package daemon_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/martintrifunov/orkestar/internal/agent"
	"github.com/martintrifunov/orkestar/internal/daemon"
	"github.com/martintrifunov/orkestar/internal/ipc"
)

// controllableAdapter and controllableSession let a test drive an agent
// session's lifecycle events directly, without depending on a real Claude
// Code or OpenCode installation.
type controllableAdapter struct {
	capabilities agent.Capabilities
	sessions     chan *controllableSession
}

func newControllableAdapter(name string) *controllableAdapter {
	return &controllableAdapter{
		capabilities: agent.Capabilities{
			Name:                name,
			SupportsInteractive: true,
			SupportsPrompt:      true,
			SupportsInterrupt:   true,
		},
		sessions: make(chan *controllableSession, 4),
	}
}

func (a *controllableAdapter) Capabilities() agent.Capabilities { return a.capabilities }

func (a *controllableAdapter) Launch(ctx context.Context, options agent.LaunchOptions) (agent.Session, error) {
	session := &controllableSession{
		id:      "native-1",
		state:   agent.StateReady,
		events:  make(chan agent.LifecycleEvent, 16),
		prompts: make(chan string, 16),
		// Recording what the daemon asked for is how a test can check where an
		// agent was started and reach the hook token it was handed.
		options: options,
	}
	a.sessions <- session
	return session, nil
}

// launched returns the session from the most recent Launch, waiting briefly
// because the daemon returns from agent.launch before the test observes it.
func (a *controllableAdapter) launched(t *testing.T) *controllableSession {
	t.Helper()
	select {
	case session := <-a.sessions:
		return session
	case <-time.After(3 * time.Second):
		t.Fatal("adapter was never asked to launch a session")
		return nil
	}
}

type controllableSession struct {
	id      string
	state   agent.State
	events  chan agent.LifecycleEvent
	prompts chan string
	options agent.LaunchOptions
}

// hookToken is the token the daemon generated for this session's hook bridge.
// A hook is only accepted when it carries it.
//
// The last entry wins, the way it does at exec time. The daemon appends its
// variables to os.Environ(), so a test run from inside an Orkestar agent pane
// sees the outer session's token first and would otherwise use it.
func (s *controllableSession) hookToken() string {
	token := ""
	for _, entry := range s.options.Environment {
		if value, ok := strings.CutPrefix(entry, "ORKESTAR_HOOK_TOKEN="); ok {
			token = value
		}
	}
	return token
}

func (s *controllableSession) ID() string              { return s.id }
func (s *controllableSession) NativeSessionID() string { return s.id }
func (s *controllableSession) State() agent.State      { return s.state }
func (s *controllableSession) Prompt(ctx context.Context, text string) error {
	select {
	case s.prompts <- text:
	default:
	}
	return nil
}
func (s *controllableSession) Interrupt(ctx context.Context) error { return nil }
func (s *controllableSession) Events() <-chan agent.LifecycleEvent { return s.events }
func (s *controllableSession) Close() error {
	close(s.events)
	return nil
}

func (s *controllableSession) emit(state agent.State, reason string) {
	s.state = state
	s.events <- agent.LifecycleEvent{State: state, Reason: reason, Timestamp: time.Now().UTC()}
}

func TestAgentLifecycleAndPermissionInbox(t *testing.T) {
	t.Parallel()

	temporaryDirectory := t.TempDir()
	socketDirectory, err := os.MkdirTemp("/tmp", "orkestar-agent-test-")
	if err != nil {
		t.Fatalf("create socket directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDirectory) })
	socketPath := filepath.Join(socketDirectory, "orkestar.sock")

	server := daemon.NewServer(socketPath)
	adapter := newControllableAdapter("fake-agent")
	server.RegisterAdapter(adapter)

	ctx, cancel := context.WithCancel(context.Background())
	serverError := make(chan error, 1)
	go func() { serverError <- server.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-serverError; err != nil {
			t.Errorf("server shutdown: %v", err)
		}
	})

	client := ipc.NewClient(socketPath)
	waitForServer(t, client)
	callContext, callCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer callCancel()

	var workspace daemon.Workspace
	if err := client.Call(callContext, "workspace.create", map[string]string{
		"directory": temporaryDirectory,
	}, &workspace); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	var launched daemon.Agent
	if err := client.Call(callContext, "agent.launch", map[string]any{
		"workspace_id": workspace.ID,
		"adapter":      "fake-agent",
		"mode":         "interactive",
	}, &launched); err != nil {
		t.Fatalf("launch agent: %v", err)
	}
	if launched.NativeSessionID != "native-1" {
		t.Fatalf("unexpected native session ID: %q", launched.NativeSessionID)
	}
	if launched.State != string(agent.StateReady) {
		t.Fatalf("unexpected initial state: %q", launched.State)
	}

	stream, err := client.OpenStream(callContext, "agent.attach", map[string]string{
		"agent_id": launched.ID,
	}, new(struct {
		Agent daemon.Agent `json:"agent"`
	}))
	if err != nil {
		t.Fatalf("attach agent: %v", err)
	}
	defer stream.Close()

	var session *controllableSession
	select {
	case session = <-adapter.sessions:
	case <-time.After(2 * time.Second):
		t.Fatal("adapter never launched a session")
	}

	session.emit(agent.StateWaitingPermission, "wants to run rm -rf")
	waitForAgentState(t, stream, string(agent.StateWaitingPermission))

	var listed struct {
		Permissions []daemon.PermissionRequest `json:"permissions"`
	}
	if err := client.Call(callContext, "permission.list", nil, &listed); err != nil {
		t.Fatalf("list permissions: %v", err)
	}
	if len(listed.Permissions) != 1 || listed.Permissions[0].AgentID != launched.ID {
		t.Fatalf("unexpected permission list: %#v", listed.Permissions)
	}
	if listed.Permissions[0].Reason != "wants to run rm -rf" {
		t.Fatalf("unexpected permission reason: %q", listed.Permissions[0].Reason)
	}

	var resolved map[string]string
	if err := client.Call(callContext, "permission.resolve", map[string]string{"permission_id": listed.Permissions[0].ID, "decision": "allow"}, &resolved); err == nil {
		t.Fatal("adapter without an approval channel must reject resolution")
	}
	select {
	case <-session.prompts:
		t.Fatal("permission decision was incorrectly sent as prompt text")
	default:
	}

	session.emit(agent.StateReady, "resumed")
	waitForAgentState(t, stream, string(agent.StateReady))

	if err := client.Call(callContext, "permission.list", nil, &listed); err != nil {
		t.Fatalf("list permissions after resolve: %v", err)
	}
	if len(listed.Permissions) != 0 {
		t.Fatalf("expected no pending permissions, got %#v", listed.Permissions)
	}

	if err := client.Call(callContext, "agent.prompt", map[string]string{
		"agent_id": launched.ID,
		"text":     "hello",
	}, &resolved); err != nil {
		t.Fatalf("prompt agent: %v", err)
	}
	select {
	case text := <-session.prompts:
		if text != "hello" {
			t.Fatalf("unexpected prompt text: %q", text)
		}
	case <-time.After(time.Second):
		t.Fatal("prompt was not forwarded to the agent session")
	}

	var snapshot daemon.Snapshot
	if err := client.Call(callContext, "system.snapshot", nil, &snapshot); err != nil {
		t.Fatalf("get snapshot: %v", err)
	}
	if len(snapshot.Agents) != 1 || snapshot.Agents[0].ID != launched.ID {
		t.Fatalf("unexpected agents in snapshot: %#v", snapshot.Agents)
	}
}

func waitForAgentState(t *testing.T, stream *ipc.Stream, want string) {
	t.Helper()

	deadline := time.After(2 * time.Second)
	for {
		type result struct {
			event ipc.Event
			err   error
		}
		done := make(chan result, 1)
		go func() {
			var event ipc.Event
			err := stream.Receive(&event)
			done <- result{event: event, err: err}
		}()

		select {
		case r := <-done:
			if r.err != nil {
				t.Fatalf("receive agent event: %v", r.err)
			}
			if r.event.Event != "agent.lifecycle" {
				continue
			}
			var agentState daemon.Agent
			if err := json.Unmarshal(r.event.Data, &agentState); err != nil {
				t.Fatalf("decode agent lifecycle payload: %v", err)
			}
			if agentState.State == want {
				return
			}
		case <-deadline:
			t.Fatalf("timed out waiting for agent state %q", want)
		}
	}
}
