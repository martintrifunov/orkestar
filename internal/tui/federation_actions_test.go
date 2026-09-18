package tui

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/martintrifunov/orkestar/internal/agent"
	"github.com/martintrifunov/orkestar/internal/daemon"
	"github.com/martintrifunov/orkestar/internal/ipc"
)

// launchTestAgent creates a workspace and starts a fake agent in it, so a
// test has a real agent record on a daemon without installing a CLI.
func launchTestAgent(t *testing.T, client *ipc.Client, adapterName string) daemon.Agent {
	t.Helper()
	ctx := context.Background()
	var workspace daemon.Workspace
	if err := client.Call(ctx, "workspace.create", map[string]string{"directory": t.TempDir()}, &workspace); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	var launched daemon.Agent
	if err := client.Call(ctx, "agent.launch", map[string]any{
		"workspace_id": workspace.ID, "adapter": adapterName, "mode": "interactive",
	}, &launched); err != nil {
		t.Fatalf("launch agent: %v", err)
	}
	return launched
}

// waitAgentState polls a daemon until an agent reports the expected state.
// Ending a session is asynchronous on the daemon side: the stop reply can
// arrive before the exit watcher has published the final state.
func waitAgentState(t *testing.T, client *ipc.Client, id, want string) daemon.Agent {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		var snapshot daemon.Snapshot
		if err := client.Call(context.Background(), "system.snapshot", nil, &snapshot); err != nil {
			t.Fatalf("snapshot: %v", err)
		}
		for _, a := range snapshot.Agents {
			if a.ID == id {
				if a.State == want {
					return a
				}
				if time.Now().After(deadline) {
					t.Fatalf("agent %s stayed %s, want %s", id, a.State, want)
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// stoppableAdapter is a fixture whose Close publishes the stopped event the
// way a PTY-backed agent's exit does. The shared FakeAdapter closes its
// channel without one, so stopping it would spin until the daemon's timeout.
type stoppableAdapter struct{}

func (a *stoppableAdapter) Capabilities() agent.Capabilities {
	return agent.Capabilities{
		Name: "fake", SupportsInteractive: true, SupportsPrompt: true,
		SupportsInterrupt: true, SupportsResume: true,
	}
}

func (a *stoppableAdapter) Launch(_ context.Context, options agent.LaunchOptions) (agent.Session, error) {
	session := &stoppableSession{nativeID: options.ResumeSessionID, events: make(chan agent.LifecycleEvent, 8)}
	if session.nativeID == "" {
		session.nativeID = "native-1"
	}
	session.emit(agent.StateReady, "launched")
	return session, nil
}

type stoppableSession struct {
	mu       sync.Mutex
	nativeID string
	state    agent.State
	events   chan agent.LifecycleEvent
	closed   bool
}

func (s *stoppableSession) ID() string                          { return "stoppable" }
func (s *stoppableSession) NativeSessionID() string             { return s.nativeID }
func (s *stoppableSession) Events() <-chan agent.LifecycleEvent { return s.events }

func (s *stoppableSession) State() agent.State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

func (s *stoppableSession) Prompt(context.Context, string) error {
	s.emit(agent.StateWorking, "prompted")
	s.emit(agent.StateReady, "prompted")
	return nil
}

func (s *stoppableSession) Interrupt(context.Context) error {
	s.emit(agent.StateReady, "interrupted")
	return nil
}

func (s *stoppableSession) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()
	s.emit(agent.StateStopped, "closed")
	close(s.events)
	return nil
}

func (s *stoppableSession) emit(state agent.State, reason string) {
	s.mu.Lock()
	if s.closed && state != agent.StateStopped {
		s.mu.Unlock()
		return
	}
	s.state = state
	s.mu.Unlock()
	event := agent.LifecycleEvent{State: state, Reason: reason, Timestamp: time.Now().UTC()}
	select {
	case s.events <- event:
	default:
	}
}

func remoteTUI(t *testing.T) (Model, *ipc.Client, *ipc.Client, daemon.Agent) {
	t.Helper()
	localClient := startEmbeddedTestDaemon(t)
	remoteClient := startEmbeddedTestDaemon(t, &stoppableAdapter{})
	launched := launchTestAgent(t, remoteClient, "fake")

	m := New(localClient, t.TempDir())
	m.width, m.height = 160, 44
	m.machines = []Machine{
		{ID: "local", Label: "Local", Client: localClient},
		{ID: "m1", Label: "Build", Client: remoteClient},
	}
	m.remote = []MachineView{{
		ID: "m1", Label: "Build", State: "online",
		Snapshot: daemon.Snapshot{Agents: []daemon.Agent{launched}},
	}}
	m.focus = focusAgents
	return m, localClient, remoteClient, launched
}

// A remote agent row is shown but could not be selected or driven; the merged
// list made the selection index meaningless past the local agents.
func TestRemoteAgentRowsAreSelectable(t *testing.T) {
	m, localClient, _, launched := remoteTUI(t)
	m.snapshot.Agents = []daemon.Agent{{ID: "local_agent", Adapter: "claude-code", State: "working"}}

	m, _ = press(t, m, 'j')
	if m.agentSelected != 1 {
		t.Fatalf("the remote row is not reachable: %d", m.agentSelected)
	}
	scoped, ok := m.scopedAgentAt(m.agentSelected)
	if !ok || !scoped.Remote || scoped.Agent.ID != launched.ID {
		t.Fatalf("the selection does not name the remote agent: %+v", scoped)
	}
	if !strings.Contains(m.renderAgents(), launched.Adapter) {
		t.Fatal("the remote row is not rendered")
	}

	// y/x answer permissions on the daemon that raised them; a remote row
	// must say so rather than resolving a local permission.
	_ = m.resolveSelectedPermission("allow")
	if !strings.Contains(m.notice, "Switch to that machine") {
		t.Fatalf("a remote permission was not redirected: %q", m.notice)
	}
	if localClient == nil {
		t.Fatal("unreachable")
	}
}

// Interrupt and stop on a remote row go to that machine's daemon, and the
// result refreshes the polled view rather than the local board.
func TestRemoteAgentActionsRouteToItsMachine(t *testing.T) {
	m, _, _, launched := remoteTUI(t)
	m.agentSelected = 0 // allAgents has only the remote agent in this setup

	m, cmd := press(t, m, 'i')
	if cmd == nil {
		t.Fatal("i did not interrupt the remote agent")
	}
	msg, ok := cmd().(lifecycleMsg)
	if !ok || msg.err != nil || !msg.foreign || msg.machineID != "m1" {
		t.Fatalf("interrupt did not route remotely: %+v", msg)
	}
	updated, _ := m.Update(msg)
	m = updated.(Model)
	if !strings.Contains(m.notice, "Interrupted") {
		t.Fatalf("the interrupt was not reported: %q", m.notice)
	}

	// Stopping takes two presses, and the second goes to the remote daemon.
	m, cmd = press(t, m, 'X')
	if cmd != nil || m.pendingStop != launched.ID {
		t.Fatal("the first X did not arm the remote stop")
	}
	m, cmd = press(t, m, 'X')
	if cmd == nil {
		t.Fatal("the second X did not stop the remote agent")
	}
	msg, ok = cmd().(lifecycleMsg)
	if !ok || msg.err != nil || !msg.foreign {
		t.Fatalf("stop did not route remotely: %+v", msg)
	}
	waitAgentState(t, m.machines[1].Client, launched.ID, "stopped")
}

// Resume on a finished remote agent runs on that machine and reports back
// without trying to open a pane here.
func TestRemoteAgentResumeRoutesToItsMachine(t *testing.T) {
	m, _, remoteClient, launched := remoteTUI(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := remoteClient.Call(ctx, "agent.stop", map[string]string{"agent_id": launched.ID}, &launched); err != nil {
		t.Fatalf("stop for resume: %v", err)
	}
	stopped := waitAgentState(t, remoteClient, launched.ID, "stopped")
	m.remote[0].Snapshot.Agents = []daemon.Agent{stopped}
	m.agentSelected = 0

	m, cmd := press(t, m, 'u')
	if cmd == nil {
		t.Fatal("u did not resume the remote agent")
	}
	msg, ok := cmd().(agentLaunchedMsg)
	if !ok || msg.err != nil || !msg.foreign || msg.machineID != "m1" {
		t.Fatalf("resume did not route remotely: %+v", msg)
	}
	updated, _ := m.Update(msg)
	m = updated.(Model)
	if !strings.Contains(m.notice, "Resumed") {
		t.Fatalf("the resume was not reported: %q", m.notice)
	}
	if m.opening {
		t.Fatal("a remote resume should not wait for a local pane")
	}
}

// Enter on a remote row moves to that machine, because the pane has to attach
// through that machine's client.
func TestOpeningARemoteAgentSwitchesMachine(t *testing.T) {
	m, _, remoteClient, launched := remoteTUI(t)
	launched.TerminalID = "term_remote"
	m.remote[0].Snapshot.Agents = []daemon.Agent{launched}
	m.agentSelected = 0

	m, cmd := press(t, m, tea.KeyEnter)
	if cmd == nil {
		t.Fatal("enter did not open the remote agent")
	}
	if m.machineIndex != 1 || m.client != remoteClient {
		t.Fatalf("the interface did not move to the remote machine: index=%d", m.machineIndex)
	}
	if !m.opening {
		t.Fatal("the pane is not being opened")
	}
}
