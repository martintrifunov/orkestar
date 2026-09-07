package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/martintrifunov/orkestar/internal/agent"
	"github.com/martintrifunov/orkestar/internal/ipc"
	"github.com/martintrifunov/orkestar/internal/workflow"
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

	// opening is a prompt to send once the agent is actually able to read
	// one, and started records that it is. An interactive CLI owns a PTY the
	// moment it is spawned but does not draw its input box for a second or
	// two, and anything written before then is typed into nothing.
	opening string
	started bool
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

// holdOpeningPrompt stores a prompt to deliver when the session signals it has
// started, and reports whether it must be sent now instead, because that
// signal already arrived. Launch and the hook that marks a session started can
// race: the agent's runtime is up early enough to call back before agent.launch
// has returned.
func (a *agentSession) holdOpeningPrompt(text string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.started {
		return true
	}
	a.opening = text
	return false
}

// takeOpeningPrompt marks the session started and hands back the prompt that
// was waiting for it, if any. It returns a prompt at most once.
func (a *agentSession) takeOpeningPrompt() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.started = true
	text := a.opening
	a.opening = ""
	return text
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

// launchParams is everything a launch needs. It is a named type because a
// template applies one without an IPC request to decode.
type launchParams struct {
	WorkspaceID     string `json:"workspace_id"`
	Adapter         string `json:"adapter"`
	Mode            string `json:"mode"`
	Columns         int    `json:"columns"`
	Rows            int    `json:"rows"`
	ResumeSessionID string `json:"resume_session_id"`
	TaskID          string `json:"task_id"`
	// Prompt is the work the agent is being launched to do. It is held
	// until the session signals it started, so a caller does not have to
	// guess how long an interactive CLI takes to come up.
	Prompt string `json:"prompt"`
}

func (s *Server) launchAgent(ctx context.Context, rawParams json.RawMessage) (Agent, error) {
	var params launchParams
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return Agent{}, fmt.Errorf("decode agent launch params: %w", err)
	}
	return s.launch(ctx, params)
}

// launchForTask starts an agent on a task, choosing the mode from what the
// adapter supports. Applying a template goes through here.
func (s *Server) launchForTask(ctx context.Context, task workflow.Task, adapterName, prompt string) (Agent, error) {
	s.mu.RLock()
	adapter, ok := s.adapters[adapterName]
	s.mu.RUnlock()
	if !ok {
		return Agent{}, fmt.Errorf("adapter %q is not registered", adapterName)
	}
	mode := string(agent.ModeManaged)
	if adapter.Capabilities().SupportsInteractive {
		mode = string(agent.ModeInteractive)
	}
	return s.launch(ctx, launchParams{
		WorkspaceID: task.WorkspaceID,
		Adapter:     adapterName,
		Mode:        mode,
		TaskID:      task.ID,
		Prompt:      prompt,
	})
}

func (s *Server) launch(ctx context.Context, params launchParams) (Agent, error) {

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

	if params.Prompt != "" {
		// Only a PTY-backed session has an interface that has to come up
		// before it can be typed at.
		_, interactive := session.(agent.ProcessSession)
		s.deliverOpeningPrompt(entry, params.Prompt, interactive)
	}

	s.agentWorkers.Add(1)
	go func() { defer s.agentWorkers.Done(); guard("agent.watch", func() { s.watchAgent(id, entry) }) }()
	return metadata, nil
}

func (s *Server) watchAgent(id string, entry *agentSession) {
	for event := range entry.liveSession().Events() {
		metadata, permissionCleared := entry.applyLifecycleEvent(event)

		s.mu.Lock()
		if s.agents[id] != entry {
			s.mu.Unlock()
			continue
		}
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
	go guard("agent.attach-reader", func() {
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
	})

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

// openingPromptTimeout bounds the wait for a session to say it started. Hooks
// are the reliable signal, and every adapter Orkestar launches is configured
// with them, but hook support depends on the agent's version and the user's
// policy. Rather than drop the work on the floor when none arrives, send it
// late and say so: an agent that missed its prompt is a session sitting idle
// with a task assigned to it and nobody watching.
const openingPromptTimeout = 30 * time.Second

// deliverOpeningPrompt sends text once the agent can read it. A session that
// already signalled started is prompted immediately; otherwise the prompt
// waits for that signal, or for openingPromptTimeout, whichever comes first.
func (s *Server) deliverOpeningPrompt(entry *agentSession, text string, interactive bool) {
	// A managed session takes a prompt as a structured call, so there is no
	// interface to wait for and nothing to settle.
	if !interactive {
		s.sendOpeningPrompt(entry, text, false)
		return
	}
	if entry.holdOpeningPrompt(text) {
		s.sendOpeningPrompt(entry, text, true)
		return
	}
	s.agentWorkers.Add(1)
	go func() {
		defer s.agentWorkers.Done()
		guard("agent.opening-prompt-timeout", func() {
			select {
			case <-time.After(openingPromptTimeout):
			case <-s.stop:
				return
			}
			if late := entry.takeOpeningPrompt(); late != "" {
				s.sendOpeningPrompt(entry, late, false)
			}
		})
	}()
}

// openingPromptSettle is a pause between an agent saying it started and being
// typed at. The start signal means its runtime is up and calling back, not
// that its terminal interface has finished drawing and is reading keys.
//
// Measured against Claude Code: text written the instant the process spawns is
// buffered and appears in its input box, but the Enter after it is discarded,
// and the prompt sits there unsubmitted. Two seconds in, the same write both
// lands and submits. This is an empirical number covering the gap after the
// signal, which is why it is not the mechanism; the signal is.
const openingPromptSettle = 1500 * time.Millisecond

// sendOpeningPrompt prompts the session, off the caller's goroutine. Both
// callers need that: the hook path is holding an agent's own hook request
// open, so writing to that agent's PTY from it risks stalling on a buffer the
// agent cannot drain until it gets its reply.
//
// A failure is recorded on the agent rather than returned. The launch it
// belongs to has already succeeded, and a caller holding that agent needs to
// find out there.
func (s *Server) sendOpeningPrompt(entry *agentSession, text string, settle bool) {
	s.agentWorkers.Add(1)
	go func() {
		defer s.agentWorkers.Done()
		guard("agent.opening-prompt", func() {
			if settle {
				select {
				case <-time.After(openingPromptSettle):
				case <-s.stop:
					return
				}
			}
			session := entry.liveSession()
			if session == nil {
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if err := session.Prompt(ctx, text); err != nil {
				entry.mu.Lock()
				entry.metadata.AttentionReason = "opening prompt failed: " + err.Error()
				entry.mu.Unlock()
			}
			_ = s.persist()
		})
	}()
}

// AgentCondition is a state a waiter is waiting for an agent session to reach.
type AgentCondition string

const (
	// AgentBlocked is the one herdr named and the one that matters most: stop
	// waiting when the agent genuinely cannot continue without someone.
	AgentBlocked AgentCondition = "blocked"
	// AgentIdle waits for the agent to finish its turn and want input.
	AgentIdle AgentCondition = "idle"
	// AgentStopped waits for the session to end, however it ends.
	AgentStopped AgentCondition = "stopped"
)

func (c AgentCondition) valid() bool {
	switch c {
	case AgentBlocked, AgentIdle, AgentStopped:
		return true
	default:
		return false
	}
}

// satisfiedBy reports whether a state meets the condition, and whether waiting
// longer is pointless. A stopped session never becomes idle or blocked again.
func (c AgentCondition) satisfiedBy(state string) (met bool, hopeless bool) {
	finished := state == "stopped" || state == "crashed" || state == "interrupted"
	switch c {
	case AgentStopped:
		return finished, false
	case AgentIdle:
		return state == string(agent.StateWaitingInput), finished
	case AgentBlocked:
		return isAttentionState(agent.State(state)), finished
	}
	return false, true
}

// waitForAgent blocks until an agent session reaches a condition, the wait
// becomes pointless, or ctx ends.
//
// It subscribes before reading the current state, so a change landing between
// the two wakes the wait instead of being missed, and it re-reads rather than
// trusting the event, so a coalesced or dropped one costs nothing.
func (s *Server) waitForAgent(ctx context.Context, agentID string, until AgentCondition) (Agent, error) {
	if !until.valid() {
		return Agent{}, fmt.Errorf("invalid wait condition %q", until)
	}
	entry, err := s.findAgent(agentID)
	if err != nil {
		return Agent{}, err
	}
	metadata, events, unsubscribe := entry.subscribe()
	defer unsubscribe()

	for {
		met, hopeless := until.satisfiedBy(metadata.State)
		switch {
		case met:
			return metadata, nil
		case hopeless:
			return metadata, fmt.Errorf("agent %q is %s and will never be %s", agentID, metadata.State, until)
		}
		select {
		case _, open := <-events:
			if !open {
				return metadata, fmt.Errorf("agent %q is no longer being watched", agentID)
			}
			metadata = entry.snapshot()
		case <-ctx.Done():
			return metadata, fmt.Errorf("waiting for agent %q to be %s: %w", agentID, until, ctx.Err())
		case <-s.stop:
			return metadata, errors.New("daemon is shutting down")
		}
	}
}
