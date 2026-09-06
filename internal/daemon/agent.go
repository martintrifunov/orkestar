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
	SignalSource    string `json:"signal_source,omitempty"`
	AttentionReason string `json:"attention_reason,omitempty"`
	// TaskID is the task this session was launched to work on, if any. It is
	// what makes an agent's activity mean something on the board: the daemon
	// starts the session in the task's worktree, assigns the task to it, and
	// moves the task to in_progress on the session's first prompt.
	TaskID string `json:"task_id,omitempty"`
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

func (a *agentSession) liveSession() agent.Session {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.session
}
func (a *agentSession) applyLifecycleEvent(event agent.LifecycleEvent) (Agent, bool) {
	return a.applyLifecycle(event, false)
}
func (a *agentSession) applyLifecycle(event agent.LifecycleEvent, hook bool) (Agent, bool) {
	return a.applyLifecycleIf(event, hook, "")
}
func (a *agentSession) applyLifecycleIf(event agent.LifecycleEvent, hook bool, expected agent.State) (Agent, bool) {
	a.mu.Lock()
	if expected != "" && a.metadata.State != string(expected) {
		metadata := a.metadata
		a.mu.Unlock()
		return metadata, false
	}
	// Process-ready is a launch acknowledgement; hooks own turn state once seen.
	if (a.metadata.State == "stopped" || a.metadata.State == "crashed") && event.State != agent.StateStopped && event.State != agent.StateCrashed ||
		(!hook && a.metadata.SignalSource == "hooks" && event.State == agent.StateReady) {
		metadata := a.metadata
		a.mu.Unlock()
		return metadata, false
	}
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
		TaskID          string `json:"task_id"`
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

	// An agent launched for a task works in that task's worktree when it has
	// one, so its changes land on the task's branch rather than in the
	// workspace's primary checkout.
	directory := workspace.Directory
	if params.TaskID != "" {
		task, terr := s.tasks.Get(params.TaskID)
		if terr != nil {
			return Agent{}, terr
		}
		if task.WorkspaceID != params.WorkspaceID {
			return Agent{}, fmt.Errorf("task %q belongs to workspace %q, not %q", task.ID, task.WorkspaceID, params.WorkspaceID)
		}
		if task.WorktreePath != "" {
			directory = task.WorktreePath
		}
	}

	id, err := newID("agent")
	if err != nil {
		return Agent{}, err
	}
	token, err := newID("hook")
	if err != nil {
		return Agent{}, err
	}
	hookCommand, env, err := s.hookOptions(id, token, params.Adapter)
	if err != nil {
		return Agent{}, err
	}
	starting := newAgentSession(Agent{ID: id, WorkspaceID: params.WorkspaceID, Adapter: params.Adapter, Mode: string(mode), TaskID: params.TaskID, State: "starting", CreatedAt: time.Now().UTC()}, nil)
	s.mu.Lock()
	s.agents[id] = starting
	s.hookTokens[id] = token
	s.mu.Unlock()
	session, err := adapter.Launch(ctx, agent.LaunchOptions{Mode: mode, Directory: directory, Columns: params.Columns, Rows: params.Rows, ResumeSessionID: params.ResumeSessionID, HookCommand: hookCommand, Environment: env})
	if err != nil {
		s.mu.Lock()
		delete(s.agents, id)
		delete(s.hookTokens, id)
		s.mu.Unlock()
		return Agent{}, fmt.Errorf("launch agent: %w", err)
	}
	metadata := Agent{
		ID:              id,
		WorkspaceID:     params.WorkspaceID,
		Adapter:         params.Adapter,
		Mode:            string(mode),
		TaskID:          params.TaskID,
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
			Directory:   directory,
			State:       "running",
			CreatedAt:   metadata.CreatedAt,
			Columns:     params.Columns, Rows: params.Rows,
		}
		terminal := newTerminalSession(terminalMetadata, processSession.Process())
		metadata.TerminalID = terminalID

		s.mu.Lock()
		s.terminals[terminalID] = terminal
		s.mu.Unlock()
	}

	entry := starting
	entry.mu.Lock()
	if entry.metadata.NativeSessionID != "" {
		metadata.NativeSessionID = entry.metadata.NativeSessionID
		metadata.State = entry.metadata.State
		metadata.SignalSource = entry.metadata.SignalSource
		metadata.AttentionReason = entry.metadata.AttentionReason
	}
	entry.metadata = metadata
	entry.session = session
	entry.mu.Unlock()

	s.mu.Lock()
	s.agents[id] = entry
	s.mu.Unlock()

	// Assigning here rather than making the caller do it in a second call
	// means the board and the session can never disagree about who owns the
	// task. A launch that raced a task removal is not worth failing over: the
	// session is already up and useful.
	if params.TaskID != "" {
		_, _ = s.tasks.Assign(params.TaskID, id)
	}

	s.agentWorkers.Add(1)
	go func() { defer s.agentWorkers.Done(); s.watchAgent(id, entry) }()
	return metadata, nil
}

func (s *Server) watchAgent(id string, entry *agentSession) {
	for event := range entry.liveSession().Events() {
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
		if metadata.State == "stopped" || metadata.State == "crashed" {
			s.cancelHookPermissions(id, "")
		}
		_ = s.persist()
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
	session := entry.liveSession()
	if session == nil {
		return nil, fmt.Errorf("agent is interrupted; resume it first")
	}
	if err := session.Prompt(ctx, params.Text); err != nil {
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
	session := entry.liveSession()
	if session == nil {
		return nil, fmt.Errorf("agent is interrupted; resume it first")
	}
	if err := session.Interrupt(ctx); err != nil {
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

// resolvePermission only sends decisions through a real approval channel.
func (s *Server) resolvePermission(ctx context.Context, raw json.RawMessage) (map[string]string, error) {
	var p struct {
		PermissionID string `json:"permission_id"`
		Decision     string `json:"decision"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	if p.Decision != "allow" && p.Decision != "deny" {
		return nil, fmt.Errorf("decision must be allow or deny")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	pending := s.pendingHooks[p.PermissionID]
	if pending == nil {
		return nil, fmt.Errorf("permission has no live reply channel; use the agent's native prompt")
	}
	select {
	case pending.decision <- p.Decision:
		delete(s.permissions, p.PermissionID)
		delete(s.pendingHooks, p.PermissionID)
	default:
		return nil, fmt.Errorf("permission already resolved")
	}
	return map[string]string{"status": "resolved"}, nil
}

func (s *Server) closeAgents() {
	defer s.agentWorkers.Wait()
	s.mu.RLock()
	entries := make([]*agentSession, 0, len(s.agents))
	for _, entry := range s.agents {
		entries = append(entries, entry)
	}
	s.mu.RUnlock()
	for _, entry := range entries {
		if session := entry.liveSession(); session != nil {
			_ = session.Close()
		}
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
