package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/martintrifunov/orkestar/internal/agent"
	"github.com/martintrifunov/orkestar/internal/ipc"
)

// Agent is the daemon's view of a running or recently-stopped agent
// session: structured lifecycle state plus whatever native identity the
// adapter exposes for resume.
type Agent struct {
	ID              string `json:"id"`
	WorkspaceID     string `json:"workspace_id"`
	Adapter         string `json:"adapter"`
	Mode            string `json:"mode"`
	NativeSessionID string `json:"native_session_id,omitempty"`
	State           string `json:"state"`
	AttentionReason string `json:"attention_reason,omitempty"`
	// TerminalID is set for interactive, PTY-backed agent sessions
	// (agent.Session implementing agent.ProcessSession): the daemon bridges
	// the underlying PTY into the same terminal buffer/subscriber machinery
	// used by plain terminal sessions, so this ID can be attached to with
	// terminal.attach exactly like any other terminal.
	TerminalID string    `json:"terminal_id,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

// PermissionRequest is a pending attention item an agent has raised that a
// human (or policy) must resolve before the agent can continue. The daemon
// aggregates these across every agent session into one inbox.
type PermissionRequest struct {
	ID        string    `json:"id"`
	AgentID   string    `json:"agent_id"`
	Reason    string    `json:"reason"`
	CreatedAt time.Time `json:"created_at"`
}

type agentEvent struct {
	Name string
	Data []byte
}

type agentSession struct {
	session agent.Session

	mu           sync.Mutex
	metadata     Agent
	subscribers  map[chan agentEvent]struct{}
	permissionID string
}

func newAgentSession(metadata Agent, session agent.Session) *agentSession {
	entry := &agentSession{
		session:     session,
		metadata:    metadata,
		subscribers: make(map[chan agentEvent]struct{}),
	}
	return entry
}

func (a *agentSession) snapshot() Agent {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.metadata
}

func (a *agentSession) subscribe() (Agent, chan agentEvent, func()) {
	a.mu.Lock()
	defer a.mu.Unlock()
	events := make(chan agentEvent, 64)
	a.subscribers[events] = struct{}{}
	unsubscribe := func() {
		a.mu.Lock()
		defer a.mu.Unlock()
		if _, ok := a.subscribers[events]; ok {
			delete(a.subscribers, events)
			close(events)
		}
	}
	return a.metadata, events, unsubscribe
}

// attentionStates are lifecycle states that require attention before the
// agent can make progress on its own.
func isAttentionState(state agent.State) bool {
	switch state {
	case agent.StateWaitingInput, agent.StateWaitingPermission, agent.StateWaitingResource:
		return true
	default:
		return false
	}
}

func (a *agentSession) applyLifecycleEvent(event agent.LifecycleEvent) (Agent, bool) {
	a.mu.Lock()
	a.metadata.State = string(event.State)
	if isAttentionState(event.State) {
		a.metadata.AttentionReason = event.Reason
	} else {
		a.metadata.AttentionReason = ""
	}
	metadata := a.metadata
	permissionCleared := a.permissionID != "" && event.State != agent.StateWaitingPermission
	if permissionCleared {
		a.permissionID = ""
	}
	a.mu.Unlock()

	payload, err := json.Marshal(metadata)
	if err != nil {
		return metadata, permissionCleared
	}
	a.broadcast(agentEvent{Name: "agent.lifecycle", Data: payload})
	return metadata, permissionCleared
}

func (a *agentSession) broadcast(event agentEvent) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for subscriber := range a.subscribers {
		select {
		case subscriber <- event:
		default:
		}
	}
}

func (a *agentSession) setPermissionID(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.permissionID = id
}

func (a *agentSession) getPermissionID() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.permissionID
}

// RegisterAdapter makes an agent adapter available for agent.launch by its
// Capabilities().Name. It must be called before Serve accepts connections
// that use it.
func (s *Server) RegisterAdapter(adapter agent.Adapter) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.adapters[adapter.Capabilities().Name] = adapter
}

func (s *Server) launchAgent(ctx context.Context, rawParams json.RawMessage) (Agent, error) {
	var params struct {
		WorkspaceID     string `json:"workspace_id"`
		Adapter         string `json:"adapter"`
		Mode            string `json:"mode"`
		Columns         int    `json:"columns"`
		Rows            int    `json:"rows"`
		ResumeSessionID string `json:"resume_session_id"`
	}
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return Agent{}, fmt.Errorf("decode agent launch params: %w", err)
	}

	s.mu.RLock()
	workspace, workspaceOK := s.workspaces[params.WorkspaceID]
	adapter, adapterOK := s.adapters[params.Adapter]
	s.mu.RUnlock()
	if !workspaceOK {
		return Agent{}, fmt.Errorf("workspace %q does not exist", params.WorkspaceID)
	}
	if !adapterOK {
		return Agent{}, fmt.Errorf("adapter %q is not registered", params.Adapter)
	}

	mode := agent.Mode(params.Mode)
	if mode == "" {
		mode = agent.ModeInteractive
	}

	session, err := adapter.Launch(ctx, agent.LaunchOptions{
		Mode:            mode,
		Directory:       workspace.Directory,
		Columns:         params.Columns,
		Rows:            params.Rows,
		ResumeSessionID: params.ResumeSessionID,
	})
	if err != nil {
		return Agent{}, fmt.Errorf("launch agent: %w", err)
	}

	id, err := newID("agent")
	if err != nil {
		return Agent{}, err
	}
	metadata := Agent{
		ID:              id,
		WorkspaceID:     params.WorkspaceID,
		Adapter:         params.Adapter,
		Mode:            string(mode),
		NativeSessionID: session.NativeSessionID(),
		State:           string(session.State()),
		CreatedAt:       time.Now().UTC(),
	}

	if processSession, ok := session.(agent.ProcessSession); ok {
		terminalID, terr := newID("term")
		if terr != nil {
			_ = session.Close()
			return Agent{}, terr
		}
		terminalMetadata := Terminal{
			ID:          terminalID,
			WorkspaceID: params.WorkspaceID,
			Command:     []string{"agent:" + params.Adapter},
			Directory:   workspace.Directory,
			State:       "running",
			CreatedAt:   metadata.CreatedAt,
		}
		terminal := newTerminalSession(terminalMetadata, processSession.Process())
		metadata.TerminalID = terminalID

		s.mu.Lock()
		s.terminals[terminalID] = terminal
		s.mu.Unlock()
	}

	entry := newAgentSession(metadata, session)

	s.mu.Lock()
	s.agents[id] = entry
	s.mu.Unlock()

	go s.watchAgent(id, entry)
	return metadata, nil
}

func (s *Server) watchAgent(id string, entry *agentSession) {
	for event := range entry.session.Events() {
		metadata, permissionCleared := entry.applyLifecycleEvent(event)

		s.mu.Lock()
		if event.State == agent.StateWaitingPermission && entry.getPermissionID() == "" {
			permissionID, err := newID("perm")
			if err == nil {
				entry.setPermissionID(permissionID)
				s.permissions[permissionID] = PermissionRequest{
					ID:        permissionID,
					AgentID:   id,
					Reason:    event.Reason,
					CreatedAt: time.Now().UTC(),
				}
			}
		} else if permissionCleared {
			for permissionID, request := range s.permissions {
				if request.AgentID == id {
					delete(s.permissions, permissionID)
				}
			}
		}
		s.mu.Unlock()
		_ = metadata
	}
}

func (s *Server) findAgent(agentID string) (*agentSession, error) {
	s.mu.RLock()
	entry, ok := s.agents[agentID]
	s.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("agent %q does not exist", agentID)
	}
	return entry, nil
}

func (s *Server) promptAgent(ctx context.Context, rawParams json.RawMessage) (map[string]string, error) {
	var params struct {
		AgentID string `json:"agent_id"`
		Text    string `json:"text"`
	}
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return nil, fmt.Errorf("decode agent prompt params: %w", err)
	}
	entry, err := s.findAgent(params.AgentID)
	if err != nil {
		return nil, err
	}
	if err := entry.session.Prompt(ctx, params.Text); err != nil {
		return nil, fmt.Errorf("prompt agent: %w", err)
	}
	return map[string]string{"status": "ok"}, nil
}

func (s *Server) interruptAgent(ctx context.Context, rawParams json.RawMessage) (map[string]string, error) {
	var params struct {
		AgentID string `json:"agent_id"`
	}
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return nil, fmt.Errorf("decode agent interrupt params: %w", err)
	}
	entry, err := s.findAgent(params.AgentID)
	if err != nil {
		return nil, err
	}
	if err := entry.session.Interrupt(ctx); err != nil {
		return nil, fmt.Errorf("interrupt agent: %w", err)
	}
	return map[string]string{"status": "ok"}, nil
}

func (s *Server) listPermissions() []PermissionRequest {
	s.mu.RLock()
	defer s.mu.RUnlock()
	permissions := make([]PermissionRequest, 0, len(s.permissions))
	for _, request := range s.permissions {
		permissions = append(permissions, request)
	}
	return permissions
}

// resolvePermission removes a pending permission request from the inbox
// and, best-effort, forwards the decision to the agent as a prompt so
// adapters without a dedicated permission-response channel still receive
// it. Adapters that gain a structured permission API can special-case this
// later without changing the IPC surface.
func (s *Server) resolvePermission(ctx context.Context, rawParams json.RawMessage) (map[string]string, error) {
	var params struct {
		PermissionID string `json:"permission_id"`
		Decision     string `json:"decision"`
	}
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return nil, fmt.Errorf("decode permission resolve params: %w", err)
	}

	s.mu.Lock()
	request, ok := s.permissions[params.PermissionID]
	if ok {
		delete(s.permissions, params.PermissionID)
	}
	s.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("permission %q does not exist", params.PermissionID)
	}

	entry, err := s.findAgent(request.AgentID)
	if err == nil {
		_ = entry.session.Prompt(ctx, params.Decision)
	}
	return map[string]string{"status": "resolved"}, nil
}

func (s *Server) closeAgents() {
	s.mu.RLock()
	entries := make([]*agentSession, 0, len(s.agents))
	for _, entry := range s.agents {
		entries = append(entries, entry)
	}
	s.mu.RUnlock()
	for _, entry := range entries {
		_ = entry.session.Close()
	}
}

func (s *Server) handleAgentAttach(connection agentConnection) {
	if connection.Request.Version != ipc.Version {
		_ = connection.Encoder.Encode(ipc.NewErrorResponse(connection.Request.ID, "unsupported_version", "unsupported protocol version"))
		return
	}
	var params struct {
		AgentID string `json:"agent_id"`
	}
	if err := json.Unmarshal(connection.Request.Params, &params); err != nil {
		_ = connection.Encoder.Encode(ipc.NewErrorResponse(connection.Request.ID, "invalid_params", "invalid agent attach params"))
		return
	}

	entry, err := s.findAgent(params.AgentID)
	if err != nil {
		_ = connection.Encoder.Encode(ipc.NewErrorResponse(connection.Request.ID, "not_found", err.Error()))
		return
	}

	metadata, events, unsubscribe := entry.subscribe()
	defer unsubscribe()

	response, err := ipc.NewResponse(connection.Request.ID, map[string]any{"agent": metadata})
	if err != nil || connection.Encoder.Encode(response) != nil {
		return
	}

	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for connection.Scanner.Scan() {
			var command struct {
				Version int    `json:"version"`
				Command string `json:"command"`
			}
			if json.Unmarshal(connection.Scanner.Bytes(), &command) != nil || command.Version != ipc.Version {
				continue
			}
			if command.Command == "detach" {
				return
			}
		}
	}()

	for {
		select {
		case <-readerDone:
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			if err := connection.Encoder.Encode(ipc.Event{Version: ipc.Version, Event: event.Name, Data: event.Data}); err != nil {
				return
			}
		}
	}
}

// agentConnection bundles the pieces handleAgentAttach needs from a live
// connection so it does not depend on net.Conn or bufio directly.
type agentConnection struct {
	Scanner interface {
		Scan() bool
		Bytes() []byte
	}
	Encoder interface{ Encode(any) error }
	Request ipc.Request
}
