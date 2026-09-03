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
	"github.com/martintrifunov/orkestar/internal/workflow"
)

var ErrAlreadyRunning = errors.New("orkestar daemon is already running")

type Workspace struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Directory string    `json:"directory"`
	CreatedAt time.Time `json:"created_at"`
}

type Snapshot struct {
	Workspaces  []Workspace         `json:"workspaces"`
	Terminals   []Terminal          `json:"terminals"`
	Agents      []Agent             `json:"agents"`
	Permissions []PermissionRequest `json:"permissions"`
	Tasks       []workflow.Task     `json:"tasks"`
}

type Server struct {
	socketPath string

	mu          sync.RWMutex
	listener    net.Listener
	workspaces  map[string]Workspace
	terminals   map[string]*terminalSession
	adapters    map[string]agent.Adapter
	agents      map[string]*agentSession
	permissions map[string]PermissionRequest
	tasks       *workflow.Board
	stop        chan struct{}
	stopOnce    sync.Once
}

func NewServer(socketPath string) *Server {
	return &Server{
		socketPath:  socketPath,
		workspaces:  make(map[string]Workspace),
		terminals:   make(map[string]*terminalSession),
		adapters:    make(map[string]agent.Adapter),
		agents:      make(map[string]*agentSession),
		permissions: make(map[string]PermissionRequest),
		tasks:       workflow.NewBoard(),
		stop:        make(chan struct{}),
	}
}

func (s *Server) Serve(ctx context.Context) error {
	if err := os.MkdirAll(filepath.Dir(s.socketPath), 0o700); err != nil {
		return fmt.Errorf("create socket directory: %w", err)
	}

	if err := s.removeStaleSocket(); err != nil {
		return err
	}

	listener, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", s.socketPath, err)
	}
	if err := os.Chmod(s.socketPath, 0o600); err != nil {
		listener.Close()
		return fmt.Errorf("restrict socket permissions: %w", err)
	}

	s.mu.Lock()
	s.listener = listener
	s.mu.Unlock()

	go func() {
		select {
		case <-ctx.Done():
		case <-s.stop:
		}
		_ = listener.Close()
	}()

	defer func() {
		s.closeTerminals()
		s.closeAgents()
		s.mu.Lock()
		s.listener = nil
		s.mu.Unlock()
		_ = os.Remove(s.socketPath)
	}()

	for {
		connection, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("accept IPC connection: %w", err)
		}
		go s.handleConnection(connection)
	}
}

func (s *Server) removeStaleSocket() error {
	connection, err := net.DialTimeout("unix", s.socketPath, 150*time.Millisecond)
	if err == nil {
		connection.Close()
		return ErrAlreadyRunning
	}
	if !errors.Is(err, os.ErrNotExist) {
		var operationError *net.OpError
		if !errors.As(err, &operationError) {
			return fmt.Errorf("probe daemon socket: %w", err)
		}
	}
	if err := os.Remove(s.socketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove stale daemon socket: %w", err)
	}
	return nil
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
		response := s.handleRequest(request)
		if err := encoder.Encode(response); err != nil {
			return
		}
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return
	}
}

func (s *Server) handleRequest(request ipc.Request) ipc.Response {
	if request.Version != ipc.Version {
		return ipc.NewErrorResponse(request.ID, "unsupported_version", fmt.Sprintf("protocol version %d is not supported", request.Version))
	}

	var (
		result any
		err    error
	)

	switch request.Method {
	case "system.ping":
		result = map[string]string{"status": "ok"}
	case "system.snapshot":
		result = s.snapshot()
	case "system.shutdown":
		result = map[string]string{"status": "stopping"}
		s.stopOnce.Do(func() { close(s.stop) })
	case "workspace.create":
		result, err = s.createWorkspace(request.Params)
	case "terminal.start":
		result, err = s.startTerminal(request.Params)
	case "agent.launch":
		result, err = s.launchAgent(context.Background(), request.Params)
	case "agent.prompt":
		result, err = s.promptAgent(context.Background(), request.Params)
	case "agent.interrupt":
		result, err = s.interruptAgent(context.Background(), request.Params)
	case "permission.list":
		result = map[string]any{"permissions": s.listPermissions()}
	case "permission.resolve":
		result, err = s.resolvePermission(context.Background(), request.Params)
	case "task.create":
		result, err = s.createTask(request.Params)
	case "task.setStatus":
		result, err = s.setTaskStatus(request.Params)
	case "task.assign":
		result, err = s.assignTask(request.Params)
	default:
		return ipc.NewErrorResponse(request.ID, "method_not_found", fmt.Sprintf("unknown method %q", request.Method))
	}

	if err != nil {
		return ipc.NewErrorResponse(request.ID, "invalid_params", err.Error())
	}
	response, err := ipc.NewResponse(request.ID, result)
	if err != nil {
		return ipc.NewErrorResponse(request.ID, "internal_error", err.Error())
	}
	return response
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
	return Snapshot{
		Workspaces:  workspaces,
		Terminals:   terminals,
		Agents:      agents,
		Permissions: permissions,
		Tasks:       s.tasks.List(),
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
