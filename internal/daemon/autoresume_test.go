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
	"github.com/martintrifunov/orkestar/internal/store"
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

// An auto-resume must survive a second crash: the resumed agent and the
// forgotten old record have to reach the database, not just memory, or a
// restart resurrects the old interrupted entry and loses the new session.
func TestAutoResumePersistsResumedAgent(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "orkestar-autoresume-persist-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	socket := filepath.Join(dir, "socket")
	server := NewServer(socket)
	server.SetAutoResume(true)
	server.RegisterAdapter(agent.NewFakeAdapter(agent.Capabilities{
		Name: "fixture", SupportsInteractive: true, SupportsResume: true,
	}))
	if err := server.openStore(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		server.closeAgents()
		if server.store != nil {
			_ = server.store.Close()
		}
	}()
	server.mu.Lock()
	server.workspaces["w1"] = Workspace{ID: "w1", Directory: dir}
	server.mu.Unlock()
	server.agents["agent_old"] = newAgentSession(Agent{
		ID: "agent_old", WorkspaceID: "w1", Adapter: "fixture", Mode: "interactive",
		State: "interrupted", NativeSessionID: "native-1",
	}, nil)
	if err := server.persist(); err != nil {
		t.Fatal(err)
	}

	server.autoResumeInterrupted()

	var resumedID string
	server.mu.RLock()
	for id, entry := range server.agents {
		if id != "agent_old" && entry.snapshot().NativeSessionID == "native-1" {
			resumedID = id
		}
	}
	server.mu.RUnlock()
	if resumedID == "" {
		t.Fatal("auto-resume did not replace the interrupted agent in memory")
	}

	db, err := store.Open(filepath.Join(dir, "metadata.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	encoded, err := db.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var saved Snapshot
	if err := json.Unmarshal(encoded, &saved); err != nil {
		t.Fatal(err)
	}
	for _, a := range saved.Agents {
		if a.ID == "agent_old" {
			t.Fatal("the old interrupted agent is still in the database after auto-resume")
		}
	}
	found := false
	for _, a := range saved.Agents {
		if a.ID == resumedID {
			found = true
		}
	}
	if !found {
		t.Fatalf("the resumed agent %q was not persisted", resumedID)
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
