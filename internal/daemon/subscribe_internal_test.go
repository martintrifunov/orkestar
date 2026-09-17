package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/martintrifunov/orkestar/internal/agent"
)

// An attach to an agent that has already ended must return instead of waiting
// forever on a channel nothing will publish to.
func TestSubscribeToAnEndedAgentReturnsAtOnce(t *testing.T) {
	entry := newAgentSession(Agent{ID: "a", State: "interrupted"}, nil)
	_, events, unsubscribe := entry.subscribe()
	defer unsubscribe()
	if _, open := <-events; open {
		t.Fatal("a finished agent must not keep an attach subscriber waiting")
	}
}

func TestSubscribeToALiveAgentStaysOpen(t *testing.T) {
	session, err := agent.NewFakeAdapter(agent.Capabilities{Name: "fixture", SupportsInteractive: true}).
		Launch(context.Background(), agent.LaunchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	entry := newAgentSession(Agent{ID: "a", State: "ready"}, session)
	_, events, unsubscribe := entry.subscribe()
	defer unsubscribe()
	select {
	case <-events:
		t.Fatal("a live agent's subscriber should stay open until it ends")
	default:
	}
}

// A resume is claimed once, so a manual and an automatic resume cannot both
// launch a session from the same interrupted record.
func TestBeginResumeIsExclusive(t *testing.T) {
	entry := newAgentSession(Agent{ID: "a", State: "interrupted"}, nil)
	if !entry.beginResume() {
		t.Fatal("the first resume claim should win")
	}
	if entry.beginResume() {
		t.Fatal("a second resume claim should be refused")
	}
	entry.endResume()
	if !entry.beginResume() {
		t.Fatal("a claim after release should win")
	}
}

// Once a session has ended the agent must not look live: prompt, interrupt and
// explain all read liveSession.
func TestEndedAgentIsNoLongerLive(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "orkestar-live-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	server, client, _ := serveRecoveryTest(t, filepath.Join(dir, "socket"))
	var workspace Workspace
	callRecovery(t, client, "workspace.create", map[string]string{"directory": dir}, &workspace)
	var launched Agent
	callRecovery(t, client, "agent.launch", map[string]any{"workspace_id": workspace.ID, "adapter": "fixture"}, &launched)

	entry, err := server.findAgent(launched.ID)
	if err != nil {
		t.Fatal(err)
	}
	session := entry.liveSession()
	if session == nil {
		t.Fatal("a launched agent should be live")
	}
	_ = session.Close()

	deadline := time.Now().Add(2 * time.Second)
	for entry.liveSession() != nil {
		if time.Now().After(deadline) {
			t.Fatal("an ended agent still reported a live session")
		}
		time.Sleep(10 * time.Millisecond)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := client.Call(ctx, "agent.prompt", map[string]string{"agent_id": launched.ID, "text": "hi"}, new(map[string]string)); err == nil {
		t.Fatal("prompting an ended agent should be refused")
	}
}
