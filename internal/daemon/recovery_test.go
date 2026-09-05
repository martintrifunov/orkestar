package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/martintrifunov/orkestar/internal/agent"
	"github.com/martintrifunov/orkestar/internal/ipc"
	"github.com/martintrifunov/orkestar/internal/workflow"
)

func serveRecoveryTest(t *testing.T, path string) (*Server, *ipc.Client, func()) {
	t.Helper()
	s := NewServer(path)
	s.RegisterAdapter(agent.NewFakeAdapter(agent.Capabilities{Name: "fixture", SupportsInteractive: true, SupportsResume: true}))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx) }()
	client := ipc.NewClient(path)
	deadline := time.Now().Add(3 * time.Second)
	for {
		ctx, c := context.WithTimeout(context.Background(), 100*time.Millisecond)
		err := client.Call(ctx, "system.ping", nil, new(map[string]string))
		c()
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("daemon startup timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("daemon shutdown blocked")
		}
	}
	t.Cleanup(stop)
	return s, client, stop
}
func callRecovery(t *testing.T, c *ipc.Client, method string, params, result any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := c.Call(ctx, method, params, result); err != nil {
		t.Fatalf("%s: %v", method, err)
	}
}
func TestMetadataRecoveryAndExplicitResume(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "orkestar-recovery-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "socket")
	_, client, stop := serveRecoveryTest(t, path)
	var w Workspace
	callRecovery(t, client, "workspace.create", map[string]string{"directory": t.TempDir()}, &w)
	var task workflow.Task
	callRecovery(t, client, "task.create", map[string]any{"workspace_id": w.ID, "title": "survive restart", "auto_review": false}, &task)
	var a Agent
	callRecovery(t, client, "agent.launch", map[string]string{"workspace_id": w.ID, "adapter": "fixture"}, &a)
	var terminal Terminal
	callRecovery(t, client, "terminal.start", map[string]any{"workspace_id": w.ID, "command": []string{"/bin/sh", "-c", "sleep 60"}}, &terminal)
	stop()
	_, client, _ = serveRecoveryTest(t, path)
	var state Snapshot
	callRecovery(t, client, "system.snapshot", nil, &state)
	if len(state.Tasks) != 1 || state.Tasks[0].ID != task.ID || len(state.Workspaces) != 1 || state.Workspaces[0].ID != w.ID {
		t.Fatalf("metadata lost: %#v", state)
	}
	if len(state.Agents) != 1 || state.Agents[0].State != "interrupted" {
		t.Fatalf("old agent incorrectly shown live: %#v", state.Agents)
	}
	if state.Terminals[0].State == "running" {
		t.Fatal("old PTY incorrectly restored as running")
	}
	var resumed Agent
	callRecovery(t, client, "agent.resume", map[string]string{"agent_id": a.ID}, &resumed)
	if resumed.ID == a.ID || resumed.NativeSessionID != a.NativeSessionID {
		t.Fatalf("resume identity incorrect: %#v", resumed)
	}
}
func TestHookLifecycleAndPermissionDecision(t *testing.T) {
	s := NewServer(filepath.Join(t.TempDir(), "socket"))
	id := "agent-test"
	s.hookTokens[id] = "secret"
	entry := newAgentSession(Agent{ID: id, State: "ready"}, nil)
	s.agents[id] = entry
	send := func(event string) (map[string]string, error) {
		b, _ := json.Marshal(HookInput{AgentID: id, Token: "secret", Event: event, NativeSessionID: "native-session", Tool: "Bash"})
		return s.hookEvent(context.Background(), b)
	}
	if _, err := send("UserPromptSubmit"); err != nil {
		t.Fatal(err)
	}
	if entry.snapshot().State != "working" || entry.snapshot().NativeSessionID != "native-session" {
		t.Fatal("hook did not update state and native identity")
	}
	result := make(chan map[string]string, 1)
	go func() { r, _ := send("PermissionRequest"); result <- r }()
	var permission string
	deadline := time.Now().Add(time.Second)
	for permission == "" {
		s.mu.RLock()
		for id := range s.permissions {
			permission = id
		}
		s.mu.RUnlock()
		if time.Now().After(deadline) {
			t.Fatal("permission not published")
		}
		time.Sleep(time.Millisecond)
	}
	bad, _ := json.Marshal(map[string]string{"permission_id": permission, "decision": "anything"})
	if _, err := s.resolvePermission(context.Background(), bad); err == nil {
		t.Fatal("invalid approval accepted")
	}
	good, _ := json.Marshal(map[string]string{"permission_id": permission, "decision": "deny"})
	if _, err := s.resolvePermission(context.Background(), good); err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-result:
		if r["decision"] != "deny" {
			t.Fatal(r)
		}
	case <-time.After(time.Second):
		t.Fatal("decision not delivered")
	}
	_, _ = send("Stop")
	if entry.snapshot().State != "waiting_input" {
		t.Fatal("turn completion not observed")
	}
	badHook, _ := json.Marshal(HookInput{AgentID: id, Token: "wrong", Event: "UserPromptSubmit"})
	if _, err := s.hookEvent(context.Background(), badHook); err == nil {
		t.Fatal("unauthenticated hook accepted")
	}
}

func TestNativePermissionResolutionAndLateHooks(t *testing.T) {
	s := NewServer(filepath.Join(t.TempDir(), "socket"))
	entry := newAgentSession(Agent{ID: "a", State: "ready"}, nil)
	s.agents["a"] = entry
	s.hookTokens["a"] = "token"
	send := func(event string) {
		b, _ := json.Marshal(HookInput{AgentID: "a", Token: "token", Event: event, PermissionID: "native-p"})
		if _, err := s.hookEvent(context.Background(), b); err != nil {
			t.Error(err)
		}
	}
	done := make(chan struct{})
	go func() { send("PermissionRequest"); close(done) }()
	deadline := time.Now().Add(time.Second)
	for len(s.listPermissions()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("permission did not appear")
		}
		time.Sleep(time.Millisecond)
	}
	send("PermissionResolved")
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("native decision did not release pending hook")
	}
	if len(s.listPermissions()) != 0 {
		t.Fatal("stale permission remained")
	}
	send("SessionEnd")
	send("UserPromptSubmit")
	entry.applyLifecycleEvent(agent.LifecycleEvent{State: agent.StateReady})
	if entry.snapshot().State != "stopped" {
		t.Fatal("late hook revived exited agent")
	}
}
