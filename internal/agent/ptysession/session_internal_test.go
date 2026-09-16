package ptysession

import (
	"testing"
	"time"

	"github.com/martintrifunov/orkestar/internal/agent"
)

// TestWatchExitContainsAPanicAndClosesEvents pins the goroutine backstop: a
// panic while watching a session's process must not escape and kill the
// daemon that launched it, and the events channel every consumer ranges over
// must still be closed. A nil process reaches that panic path.
func TestWatchExitContainsAPanicAndClosesEvents(t *testing.T) {
	session := &Session{id: "fixture_1", events: make(chan agent.LifecycleEvent, 16)}

	done := make(chan struct{})
	go func() {
		defer close(done)
		session.watchExit()
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("watchExit did not return after a panic")
	}

	if _, ok := <-session.events; ok {
		t.Fatal("expected the events channel to be closed")
	}

	session.mu.Lock()
	closed := session.closed
	session.mu.Unlock()
	if !closed {
		t.Fatal("expected the session to be marked closed")
	}
}
