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
	"strconv"
	"sync"
	"sync/atomic"
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
	mutationMu   sync.RWMutex
	requests     sync.WaitGroup
	connections  map[net.Conn]struct{}
	// persistErr is the last metadata save failure. Mutations are applied in
	// memory before they are written, so while this is set the daemon's
	// memory is ahead of its disk; system.ping reports it and a retry loop
	// keeps trying until the save succeeds.
	persistErr atomic.Pointer[string]
	// autoResume relaunches agents that were running when the daemon last
	// stopped, once the first client connects after a restart.
	autoResume     bool
	autoResumeOnce sync.Once
	// paneHistory persists bounded terminal text across a restart. Off by
	// default: terminal output can hold secrets, tokens and prompts.
	paneHistory bool
	// adapterLoader rebuilds the full adapter set, built-ins and manifests,
	// when a reload is requested. Nil means reload is not configured.
	adapterLoader func() ([]agent.Adapter, error)

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

// SetAutoResume controls whether agents that were running when the daemon last
// stopped are relaunched when the first client connects. Call before Serve.
func (s *Server) SetAutoResume(enabled bool) { s.autoResume = enabled }

// SetPaneHistory controls whether bounded terminal text is persisted across a
// daemon restart. Call before Serve. Off by default because pane output can
// contain secrets.
func (s *Server) SetPaneHistory(enabled bool) { s.paneHistory = enabled }

// SetAdapterLoader supplies the function a reload uses to rebuild the whole
// adapter set. Rebuilding from scratch is what lets a removed or changed
// manifest take effect, rather than only adding to what is registered.
func (s *Server) SetAdapterLoader(loader func() ([]agent.Adapter, error)) { s.adapterLoader = loader }

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

	go guard("ipc.listener-shutdown", func() {
		select {
		case <-ctx.Done():
			s.stopOnce.Do(func() { close(s.stop) })
		case <-s.stop:
		}
		_ = listener.Close()
	})
	go guard("persist.retry", s.persistRetryLoop)

	defer func() {
		s.stopOnce.Do(func() { close(s.stop) })
		s.mu.Lock()
		for conn := range s.connections {
			_ = conn.Close()
		}
		s.mu.Unlock()
		// Read every live terminal's text before its process and screen are
		// closed, so an opt-in restart has something to show.
		s.savePaneHistory()
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
		// The first client to arrive after a restart is what brings the
		// interrupted agent conversations back. Nothing is relaunched while
		// nobody is connected, and an opt-out leaves this a no-op.
		s.autoResumeOnce.Do(func() {
			if s.autoResume {
				go guard("agent.auto-resume", s.autoResumeInterrupted)
			}
		})
		s.requests.Add(1)
		go func() {
			defer s.requests.Done()
			defer func() { s.mu.Lock(); delete(s.connections, connection); s.mu.Unlock() }()
			guard("ipc.connection", func() { s.handleConnection(connection) })
		}()
	}
}

func (s *Server) handleConnection(connection net.Conn) {
	defer connection.Close()

	// A wait sits for minutes without answering, and a client that goes away
	// meanwhile must release it rather than leak a subscriber until its
	// timeout, so requests are answered as they complete instead of in read
	// order. Nothing pipelines: one Call holds its conversation until the
	// reply arrives, and the attach methods below take the connection over,
	// so answering out of order only happens when nobody is listening.
	// Cancelling here is what ends the handlers below; closing the
	// connection unblocks the read loop, which is the only thing that can
	// notice the client is gone while a wait is outstanding.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var encoderMu sync.Mutex

	scanner := bufio.NewScanner(connection)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	encoder := json.NewEncoder(responseWriter{connection})
	encode := func(response ipc.Response) error {
		encoderMu.Lock()
		defer encoderMu.Unlock()
		return encoder.Encode(response)
	}
	for scanner.Scan() {
		var request ipc.Request
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
			_ = encode(ipc.NewErrorResponse("", "invalid_request", "request is not valid JSON"))
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
		if request.Method == "task.attach" {
			s.handleTaskAttach(connection, scanner, encoder, request)
			return
		}
		s.requests.Add(1)
		go func(request ipc.Request) {
			defer s.requests.Done()
			response, shutdown := s.handleRequest(ctx, request)
			encodeErr := encode(response)
			if shutdown {
				// Only now: stopping the server closes this connection, which would
				// otherwise race the acknowledgement and hand the client an EOF.
				s.stopOnce.Do(func() { close(s.stop) })
			}
			if encodeErr != nil {
				// Nobody is listening anymore; fail the read loop so the
				// cancellation above releases the other requests in flight.
				_ = connection.Close()
			}
		}(request)
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return
	}
}

// handleRequest returns the response and whether the caller must stop the
// server once that response has been written.
func (s *Server) handleRequest(ctx context.Context, request ipc.Request) (ipc.Response, bool) {
	if request.Version != ipc.Version {
		return ipc.NewErrorResponse(request.ID, "unsupported_version", fmt.Sprintf("protocol version %d is not supported", request.Version)), false
	}

	// Reset excludes mutations already in flight and new launches. Hooks and
	// waits must remain free to complete while a mutation is running.
	if !readOnlyMethod(request.Method) && request.Method != "system.reset" && request.Method != "agent.hook" && request.Method != "permission.resolve" {
		s.mutationMu.RLock()
		defer s.mutationMu.RUnlock()
	}
	var (
		result   any
		err      error
		shutdown bool
	)

	switch request.Method {
	case "system.ping":
		status := map[string]string{"status": "ok", "version": s.buildVersion, "protocol": strconv.Itoa(ipc.Version)}
		if last := s.lastPersistError(); last != "" {
			// The daemon is serving from memory that is not on disk yet;
			// saying so is better than a status line that looks healthy.
			status["persist_error"] = last
		}
		result = status
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
	case "terminal.read":
		result, err = s.terminalRead(request.Params)
	case "terminal.send":
		result, err = s.terminalSend(request.Params)
	case "terminal.wait":
		result, err = s.waitForTerminal(ctx, request.Params)
	case "terminal.start":
		result, err = s.startTerminal(request.Params)
	case "terminal.stop":
		result, err = s.stopTerminal(request.Params)
	case "terminal.remove":
		result, err = s.removeTerminal(request.Params)
	case "agent.launch":
		result, err = s.launchAgent(ctx, request.Params)
	case "agent.hook":
		result, err = s.hookEvent(ctx, request.Params)
	case "agent.resume":
		result, err = s.resumeAgent(ctx, request.Params)
	case "agent.explain":
		result, err = s.explainAgent(request.Params)
	case "agent.reloadAdapters":
		result, err = s.reloadAdapters()
	case "agent.prompt":
		result, err = s.promptAgent(ctx, request.Params)
	case "agent.interrupt":
		result, err = s.interruptAgent(ctx, request.Params)
	case "agent.stop":
		result, err = s.stopAgent(request.Params)
	case "agent.remove":
		result, err = s.removeAgent(request.Params)
	case "permission.list":
		result = map[string]any{"permissions": s.listPermissions()}
	case "permission.resolve":
		result, err = s.resolvePermission(ctx, request.Params)
	case "task.create":
		result, err = s.createTask(request.Params)
	case "template.list":
		result, err = s.listTemplates(request.Params)
	case "template.apply":
		result, err = s.applyTemplate(ctx, request.Params)
	case "task.wait":
		result, err = s.waitForTask(ctx, request.Params)
	case "agent.wait":
		result, err = s.waitForAgentSession(ctx, request.Params)
	case "task.update":
		result, err = s.updateTask(request.Params)
	case "task.setStatus":
		result, err = s.setTaskStatus(ctx, request.Params)
	case "task.assign":
		result, err = s.assignTask(request.Params)
	case "task.createWorktree":
		result, err = s.createTaskWorktree(ctx, request.Params)
	case "task.removeWorktree":
		result, err = s.removeTaskWorktree(ctx, request.Params)
	case "task.diff":
		result, err = s.taskDiff(ctx, request.Params)
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
	if err == nil && !readOnlyMethod(request.Method) && !volatileMethod(request.Method) {
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

// readOnlyMethod names the methods that change nothing, so a reply does not
// rewrite the whole snapshot to disk. The waits belong here for a second
// reason: one can sit for minutes and then persist state it never touched.
func readOnlyMethod(method string) bool {
	switch method {
	case "system.snapshot", "system.ping", "system.shutdown", "terminal.history",
		"terminal.read", "terminal.wait", "task.wait", "agent.wait", "agent.explain",
		"template.list", "permission.list":
		return true
	default:
		return false
	}
}

// volatileMethod names methods that change only live process or screen state,
// never the persisted snapshot. They are not read-only — terminal.send drives
// a running process — but there is nothing durable to save, and rewriting the
// snapshot for every keystroke would hold the persistence lock against real
// lifecycle writes.
func volatileMethod(method string) bool {
	switch method {
	case "terminal.send", "agent.reloadAdapters":
		return true
	default:
		return false
	}
}

func (s *Server) snapshot() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	workspaces := make([]Workspace, 0, len(s.workspaces))
	for _, workspace := range s.workspaces {
		workspaces = append(workspaces, workspace)
	}
	sort.Slice(workspaces, func(left, right int) bool {
		if workspaces[left].CreatedAt.Equal(workspaces[right].CreatedAt) {
			return workspaces[left].ID < workspaces[right].ID
		}
		return workspaces[left].CreatedAt.Before(workspaces[right].CreatedAt)
	})
	terminals := make([]Terminal, 0, len(s.terminals))
	for _, session := range s.terminals {
		terminals = append(terminals, session.snapshot())
	}
	sort.Slice(terminals, func(left, right int) bool {
		if terminals[left].CreatedAt.Equal(terminals[right].CreatedAt) {
			return terminals[left].ID < terminals[right].ID
		}
		return terminals[left].CreatedAt.Before(terminals[right].CreatedAt)
	})
	agents := make([]Agent, 0, len(s.agents))
	for _, entry := range s.agents {
		agents = append(agents, entry.snapshot())
	}
	sort.Slice(agents, func(left, right int) bool {
		if agents[left].CreatedAt.Equal(agents[right].CreatedAt) {
			return agents[left].ID < agents[right].ID
		}
		return agents[left].CreatedAt.Before(agents[right].CreatedAt)
	})
	permissions := make([]PermissionRequest, 0, len(s.permissions))
	for _, request := range s.permissions {
		permissions = append(permissions, request)
	}
	sort.Slice(permissions, func(left, right int) bool {
		if permissions[left].CreatedAt.Equal(permissions[right].CreatedAt) {
			return permissions[left].ID < permissions[right].ID
		}
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

	// Reuse the workspace already rooted here. A caller cannot know whether
	// one exists — an agent delegating work calls this before every task — and
	// creating a second would scatter that work across two workspaces that
	// mean the same directory. The name of the existing one stands: it is
	// where the tasks already are.
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.workspaces {
		if existing.Directory == directory {
			return existing, nil
		}
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

	s.workspaces[workspace.ID] = workspace
	return workspace, nil
}

func newID(prefix string) (string, error) {
	bytes := make([]byte, 6)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate ID: %w", err)
	}
	return prefix + "_" + hex.EncodeToString(bytes), nil
}

// Every reply, including idle-stream control replies, gets a fresh deadline.
type responseWriter struct{ net.Conn }

func (w responseWriter) Write(p []byte) (int, error) {
	if err := w.SetWriteDeadline(time.Now().Add(2 * time.Second)); err != nil {
		return 0, err
	}
	return w.Conn.Write(p)
}
