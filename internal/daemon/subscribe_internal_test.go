package daemon

import (
	"context"
	"testing"

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
