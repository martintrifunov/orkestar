package daemon

import (
	"testing"
	"time"
)

// TestRenderOutputSurvivesAPanicAndUnlocksTheSession pins the mutex half of
// the fix: renderOutput must release s.mu even when the emulator write
// panics, or every later call touching this session deadlocks.
func TestRenderOutputSurvivesAPanicAndUnlocksTheSession(t *testing.T) {
	s := newTerminalSession(Terminal{ID: "term_1", Columns: 80, Rows: 24}, nil)

	err := recoverPanic(s.metadata.ID, func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		panic("simulated emulator panic")
	})
	if err == nil {
		t.Fatal("expected the simulated panic to be recovered")
	}

	done := make(chan struct{})
	go func() {
		s.mu.Lock()
		s.mu.Unlock()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("s.mu is still locked after a recovered panic; renderOutput must release it")
	}
}

func TestRenderOutputHappyPath(t *testing.T) {
	s := newTerminalSession(Terminal{ID: "term_1", Columns: 80, Rows: 24}, nil)
	if err := s.renderOutput([]byte("hello\r\n")); err != nil {
		t.Fatalf("renderOutput on ordinary output returned an error: %v", err)
	}
}
