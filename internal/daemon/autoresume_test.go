package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/martintrifunov/orkestar/internal/agent"
	"github.com/martintrifunov/orkestar/internal/ipc"
)

// serveAutoResumeTest seeds the state a restart would restore — a workspace
// and an interrupted agent that reported a native session — then serves it.
func serveAutoResumeTest(t *testing.T, enabled bool) (*Server, *ipc.Client) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "orkestar-autoresume-")
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(dir, "socket")
	server := NewServer(socket)
	server.SetAutoResume(enabled)
	server.RegisterAdapter(agent.NewFakeAdapter(agent.Capabilities{
		Name: "fixture", SupportsInteractive: true, SupportsResume: true,
	}))
	server.mu.Lock()
	server.workspaces["w1"] = Workspace{ID: "w1", Directory: dir}
	server.mu.Unlock()
	server.agents["agent_old"] = newAgentSession(Agent{
		ID: "agent_old", WorkspaceID: "w1", Adapter: "fixture", Mode: "interactive",
		State: "interrupted", NativeSessionID: "native-1",
	}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("server shutdown: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("server did not stop")
		}
		_ = os.RemoveAll(dir)
	})
	return server, ipc.NewClient(socket)
}

func pingUntilAccepted(t *testing.T, client *ipc.Client) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		err := client.Call(ctx, "system.ping", nil, new(map[string]string))
		cancel()
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("daemon did not accept a connection")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestAutoResumeOnFirstClientAttach(t *testing.T) {
	server, client := serveAutoResumeTest(t, true)
	pingUntilAccepted(t, client)

	deadline := time.Now().Add(2 * time.Second)
	for {
		server.mu.RLock()
		resumed := false
		for id, entry := range server.agents {
			metadata := entry.snapshot()
			if id != "agent_old" && metadata.NativeSessionID == "native-1" && metadata.State != "interrupted" {
				resumed = true
			}
		}
		server.mu.RUnlock()
		if resumed {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("interrupted agent was not auto-resumed when a client connected")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestAutoResumeDisabledLeavesAgentsInterrupted(t *testing.T) {
	server, client := serveAutoResumeTest(t, false)
	pingUntilAccepted(t, client)
	time.Sleep(200 * time.Millisecond)

	server.mu.RLock()
	entry := server.agents["agent_old"]
	server.mu.RUnlock()
	if entry == nil {
		t.Fatal("the interrupted agent disappeared with auto-resume off")
	}
	if state := entry.snapshot().State; state != "interrupted" {
		t.Fatalf("agent should have stayed interrupted, got %q", state)
	}
}
