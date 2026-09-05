package daemon

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/martintrifunov/orkestar/internal/agent"
	"github.com/martintrifunov/orkestar/internal/ipc"
	"github.com/martintrifunov/orkestar/internal/store"
	"github.com/martintrifunov/orkestar/internal/workflow"
)

var ErrAlreadyRunning = ipc.ErrAlreadyRunning

type Workspace struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Directory string    `json:"directory"`
	CreatedAt time.Time `json:"created_at"`
}

type Snapshot struct {
	Workspaces  []Workspace          `json:"workspaces"`
	Terminals   []Terminal           `json:"terminals"`
	Agents      []Agent              `json:"agents"`
	Permissions []PermissionRequest  `json:"permissions"`
	Tasks       []workflow.Task      `json:"tasks"`
	Leases      []workflow.Lease     `json:"leases"`
	Artifacts   []workflow.Artifact  `json:"artifacts"`
	Adapters    []agent.Capabilities `json:"adapters"`
}

type Server struct {
	buildVersion string
	hookTokens   map[string]string
	pendingHooks map[string]*hookPermission
	agentWorkers sync.WaitGroup
	socketPath   string
	store        store.Store
	persistMu    sync.Mutex
	requests     sync.WaitGroup
	connections  map[net.Conn]struct{}

	mu              sync.RWMutex
	listener        net.Listener
	workspaces      map[string]Workspace
	terminals       map[string]*terminalSession
	adapters        map[string]agent.Adapter
	agents          map[string]*agentSession
	permissions     map[string]PermissionRequest
	tasks           *workflow.Board
	leases          *workflow.LeaseManager
	artifacts       *workflow.ArtifactStore
	reviewerAdapter string
	stop            chan struct{}
	stopOnce        sync.Once
}

// SetVersion sets the build identity advertised by ping. Call before Serve.
func (s *Server) SetVersion(version string) { s.buildVersion = version }

func NewServer(socketPath string) *Server {
	return &Server{
		hookTokens: make(map[string]string), pendingHooks: make(map[string]*hookPermission),
		socketPath:  socketPath,
		connections: make(map[net.Conn]struct{}),
		workspaces:  make(map[string]Workspace),
		terminals:   make(map[string]*terminalSession),
		adapters:    make(map[string]agent.Adapter),
		agents:      make(map[string]*agentSession),
		permissions: make(map[string]PermissionRequest),
		tasks:       workflow.NewBoard(),
		leases:      workflow.NewLeaseManager(),
		artifacts:   workflow.NewArtifactStore(),
		stop:        make(chan struct{}),
	}
}

func (s *Server) Serve(ctx context.Context) error {
	if err := os.MkdirAll(filepath.Dir(s.socketPath), 0o700); err != nil {
		return fmt.Errorf("create socket directory: %w", err)
	}

	if err := ipc.PrepareListener(s.socketPath); err != nil {
		return err
	}

	listener, err := ipc.Listen(s.socketPath)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", s.socketPath, err)
	}
	if err := ipc.RestrictListener(s.socketPath); err != nil {
		listener.Close()
		return fmt.Errorf("restrict socket permissions: %w", err)
	}

	if err := s.openStore(); err != nil {
		_ = listener.Close()
		s.persistMu.Lock()
		if s.store != nil {
			_ = s.store.Close()
			s.store = nil
		}
		s.persistMu.Unlock()
		return err
	}
	s.mu.Lock()
	s.listener = listener
	s.mu.Unlock()

	go func() {
		select {
		case <-ctx.Done():
			s.stopOnce.Do(func() { close(s.stop) })
		case <-s.stop:
		}
		_ = listener.Close()
	}()

	defer func() {
		s.stopOnce.Do(func() { close(s.stop) })
		s.mu.Lock()
		for conn := range s.connections {
			_ = conn.Close()
		}
		s.mu.Unlock()
		s.closeTerminals()
		s.requests.Wait()
		s.closeTerminals() // Include launches that were already in flight.
		s.closeAgents()
		_ = s.persist()
		s.persistMu.Lock()
		if s.store != nil {
			_ = s.store.Close()
			s.store = nil
		}
		s.persistMu.Unlock()
		s.mu.Lock()
		s.listener = nil
		s.mu.Unlock()
		_ = ipc.RemoveListener(s.socketPath)
	}()

	for {
		connection, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("accept IPC connection: %w", err)
		}
		s.mu.Lock()
		s.connections[connection] = struct{}{}
		s.mu.Unlock()
		s.requests.Add(1)
		go func() {
			defer s.requests.Done()
			defer func() { s.mu.Lock(); delete(s.connections, connection); s.mu.Unlock() }()
			s.handleConnection(connection)
		}()
	}
}

func (s *Server) handleConnection(connection net.Conn) {
	defer connection.Close()

	scanner := bufio.NewScanner(connection)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	encoder := json.NewEncoder(connection)
	for scanner.Scan() {
		var request ipc.Request
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			_ = encoder.Encode(ipc.NewErrorResponse("", "invalid_request", "request is not valid JSON"))
			continue
		}
		if request.Method == "terminal.attach" {
			s.handleTerminalAttach(connection, scanner, encoder, request)
			return
		}
		if request.Method == "agent.attach" {
			s.handleAgentAttach(agentConnection{Scanner: scanner, Encoder: encoder, Request: request})
			return
		}
		response, shutdown := s.handleRequest(request)
		encodeErr := encoder.Encode(response)
		if shutdown {
			// Only now: stopping the server closes this connection, which would
			// otherwise race the acknowledgement and hand the client an EOF.
			s.stopOnce.Do(func() { close(s.stop) })
		}
		if encodeErr != nil {
			return
		}
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return
	}
}

// handleRequest returns the response and whether the caller must stop the
// server once that response has been written.
func (s *Server) handleRequest(request ipc.Request) (ipc.Response, bool) {
	if request.Version != ipc.Version {
		return ipc.NewErrorResponse(request.ID, "unsupported_version", fmt.Sprintf("protocol version %d is not supported", request.Version)), false
	}

	var (
		result   any
		err      error
		shutdown bool
	)

	switch request.Method {
	case "system.ping":
		result = map[string]string{"status": "ok", "version": s.buildVersion}
	case "system.snapshot":
		result = s.snapshot()
	case "system.reset":
		result, err = s.resetState(request.Params)
	case "system.shutdown":
		result = map[string]string{"status": "stopping"}
		shutdown = true
	case "workspace.create":
		result, err = s.createWorkspace(request.Params)
	case "terminal.history":
		result, err = s.terminalHistory(request.Params)
	case "terminal.start":
		result, err = s.startTerminal(request.Params)
	case "terminal.stop":
		result, err = s.stopTerminal(request.Params)
	case "terminal.remove":
		result, err = s.removeTerminal(request.Params)
	case "agent.launch":
		result, err = s.launchAgent(context.Background(), request.Params)
	case "agent.hook":
		result, err = s.hookEvent(context.Background(), request.Params)
	case "agent.resume":
		result, err = s.resumeAgent(context.Background(), request.Params)
	case "agent.prompt":
		result, err = s.promptAgent(context.Background(), request.Params)
	case "agent.interrupt":
		result, err = s.interruptAgent(context.Background(), request.Params)
	case "agent.stop":
		result, err = s.stopAgent(request.Params)
	case "agent.remove":
		result, err = s.removeAgent(request.Params)
	case "permission.list":
		result = map[string]any{"permissions": s.listPermissions()}
	case "permission.resolve":
		result, err = s.resolvePermission(context.Background(), request.Params)
	case "task.create":
		result, err = s.createTask(request.Params)
	case "task.setStatus":
		result, err = s.setTaskStatus(context.Background(), request.Params)
	case "task.assign":
		result, err = s.assignTask(request.Params)
	case "task.createWorktree":
		result, err = s.createTaskWorktree(context.Background(), request.Params)
	case "task.removeWorktree":
		result, err = s.removeTaskWorktree(context.Background(), request.Params)
	case "task.diff":
		result, err = s.taskDiff(context.Background(), request.Params)
	case "resource.acquire":
		result, err = s.acquireLease(request.Params)
	case "resource.release":
		result, err = s.releaseLease(request.Params)
	case "artifact.create":
		result, err = s.createArtifact(request.Params)
	default:
		return ipc.NewErrorResponse(request.ID, "method_not_found", fmt.Sprintf("unknown method %q", request.Method)), false
	}

	if err != nil {
		return ipc.NewErrorResponse(request.ID, "invalid_params", err.Error()), shutdown
	}
	if err == nil && request.Method != "system.snapshot" && request.Method != "system.ping" && request.Method != "system.shutdown" && request.Method != "terminal.history" {
		err = s.persist()
		if err != nil {
			return ipc.NewErrorResponse(request.ID, "storage_error", err.Error()), shutdown
		}
	}
	response, err := ipc.NewResponse(request.ID, result)
	if err != nil {
		return ipc.NewErrorResponse(request.ID, "internal_error", err.Error()), shutdown
	}
	return response, shutdown
}

func (s *Server) snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	workspaces := make([]Workspace, 0, len(s.workspaces))
	for _, workspace := range s.workspaces {
		workspaces = append(workspaces, workspace)
	}
	sort.Slice(workspaces, func(left, right int) bool {
		return workspaces[left].CreatedAt.Before(workspaces[right].CreatedAt)
	})
	terminals := make([]Terminal, 0, len(s.terminals))
	for _, session := range s.terminals {
		terminals = append(terminals, session.snapshot())
	}
	sort.Slice(terminals, func(left, right int) bool {
		return terminals[left].CreatedAt.Before(terminals[right].CreatedAt)
	})
	agents := make([]Agent, 0, len(s.agents))
	for _, entry := range s.agents {
		agents = append(agents, entry.snapshot())
	}
	sort.Slice(agents, func(left, right int) bool {
		return agents[left].CreatedAt.Before(agents[right].CreatedAt)
	})
	permissions := make([]PermissionRequest, 0, len(s.permissions))
	for _, request := range s.permissions {
		permissions = append(permissions, request)
	}
	sort.Slice(permissions, func(left, right int) bool {
		return permissions[left].CreatedAt.Before(permissions[right].CreatedAt)
	})
	adapters := make([]agent.Capabilities, 0, len(s.adapters))
	for _, registered := range s.adapters {
		adapters = append(adapters, registered.Capabilities())
	}
	sort.Slice(adapters, func(left, right int) bool {
		return adapters[left].Name < adapters[right].Name
	})

	return Snapshot{
		Workspaces:  workspaces,
		Terminals:   terminals,
		Agents:      agents,
		Permissions: permissions,
		Tasks:       s.tasks.List(),
		Leases:      s.leases.ListAll(),
		Artifacts:   s.artifacts.List(),
		Adapters:    adapters,
	}
}

func (s *Server) closeTerminals() {
	s.mu.RLock()
	terminals := make([]*terminalSession, 0, len(s.terminals))
	for _, session := range s.terminals {
		terminals = append(terminals, session)
	}
	s.mu.RUnlock()
	for _, session := range terminals {
		_ = session.close()
	}
}

func (s *Server) createWorkspace(rawParams json.RawMessage) (Workspace, error) {
	var params struct {
		Directory string `json:"directory"`
		Name      string `json:"name"`
	}
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return Workspace{}, fmt.Errorf("decode workspace params: %w", err)
	}
	if params.Directory == "" {
		return Workspace{}, errors.New("workspace directory is required")
	}

	directory, err := filepath.Abs(params.Directory)
	if err != nil {
		return Workspace{}, fmt.Errorf("resolve workspace directory: %w", err)
	}
	info, err := os.Stat(directory)
	if err != nil {
		return Workspace{}, fmt.Errorf("inspect workspace directory: %w", err)
	}
	if !info.IsDir() {
		return Workspace{}, fmt.Errorf("workspace path %q is not a directory", directory)
	}
	if params.Name == "" {
		params.Name = filepath.Base(directory)
	}

	id, err := newID("w")
	if err != nil {
		return Workspace{}, err
	}
	workspace := Workspace{
		ID:        id,
		Name:      params.Name,
		Directory: directory,
		CreatedAt: time.Now().UTC(),
	}

	s.mu.Lock()
	s.workspaces[workspace.ID] = workspace
	s.mu.Unlock()
	return workspace, nil
}

func newID(prefix string) (string, error) {
	bytes := make([]byte, 6)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate ID: %w", err)
	}
	return prefix + "_" + hex.EncodeToString(bytes), nil
}
