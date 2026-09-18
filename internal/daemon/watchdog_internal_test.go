package daemon

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/martintrifunov/orkestar/internal/agent"
)

func TestWorkingSinceTracksTurnState(t *testing.T) {
	entry := newAgentSession(Agent{ID: "agent_1", State: "ready"}, nil)
	entry.applyLifecycle(agent.LifecycleEvent{State: agent.StateWorking, Timestamp: time.Now().UTC()}, false)
	entry.mu.Lock()
	started := entry.workingSince
	entry.mu.Unlock()
	if started.IsZero() {
		t.Fatal("a working state did not start the turn clock")
	}

	// A repeated working event is the same turn, not a new one.
	entry.applyLifecycle(agent.LifecycleEvent{State: agent.StateWorking, Timestamp: time.Now().UTC()}, false)
	entry.mu.Lock()
	repeated := entry.workingSince
	entry.mu.Unlock()
	if !repeated.Equal(started) {
		t.Fatal("a repeated working event reset the turn clock")
	}

	entry.applyLifecycle(agent.LifecycleEvent{State: agent.StateReady, Timestamp: time.Now().UTC()}, false)
	entry.mu.Lock()
	cleared := entry.workingSince
	entry.mu.Unlock()
	if !cleared.IsZero() {
		t.Fatal("leaving the working state did not clear the turn clock")
	}
}

func TestWatchdogFlagsALongWorkingTurn(t *testing.T) {
	s := NewServer(filepath.Join(t.TempDir(), "orkestar.sock"))
	s.SetAgentWatchdog(10 * time.Millisecond)
	entry := newAgentSession(Agent{ID: "agent_1", State: "working"}, nil)
	entry.mu.Lock()
	entry.workingSince = time.Now().Add(-time.Hour)
	entry.mu.Unlock()
	s.agents["agent_1"] = entry

	s.checkAgentDurations()
	metadata := entry.snapshot()
	if !strings.Contains(metadata.AttentionReason, "working for") {
		t.Fatalf("the long turn was not flagged: %+v", metadata)
	}
	// The flag is not rewritten on the next tick: the row already says it.
	s.checkAgentDurations()
	if again := entry.snapshot(); again.AttentionReason != metadata.AttentionReason {
		t.Fatalf("the notice changed on a second tick: %q then %q", metadata.AttentionReason, again.AttentionReason)
	}
}

func TestWatchdogLeavesIdleSessionsAndRealAttentionAlone(t *testing.T) {
	s := NewServer(filepath.Join(t.TempDir(), "orkestar.sock"))
	s.SetAgentWatchdog(10 * time.Millisecond)

	idle := newAgentSession(Agent{ID: "idle", State: "ready"}, nil)
	idle.mu.Lock()
	idle.workingSince = time.Now().Add(-time.Hour)
	idle.mu.Unlock()
	s.agents["idle"] = idle

	flagged := newAgentSession(Agent{ID: "flagged", State: "working", AttentionReason: "needs approval"}, nil)
	flagged.mu.Lock()
	flagged.workingSince = time.Now().Add(-time.Hour)
	flagged.mu.Unlock()
	s.agents["flagged"] = flagged

	s.checkAgentDurations()
	if reason := idle.snapshot().AttentionReason; reason != "" {
		t.Fatalf("an idle session was flagged: %q", reason)
	}
	if reason := flagged.snapshot().AttentionReason; reason != "needs approval" {
		t.Fatalf("a real attention reason was overwritten: %q", reason)
	}

	// Disabled means disabled, even for a turn that is clearly too long.
	s.SetAgentWatchdog(0)
	long := newAgentSession(Agent{ID: "long", State: "working"}, nil)
	long.mu.Lock()
	long.workingSince = time.Now().Add(-24 * time.Hour)
	long.mu.Unlock()
	s.agents["long"] = long
	s.checkAgentDurations()
	if reason := long.snapshot().AttentionReason; reason != "" {
		t.Fatalf("the watchdog ran while disabled: %q", reason)
	}
}
