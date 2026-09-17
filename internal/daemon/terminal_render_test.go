package daemon

import (
	"encoding/json"
	"path/filepath"
	"strings"
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
		// TryLock reports whether the mutex is free: if it is, renderOutput
		// released it after the recovered panic.
		if s.mu.TryLock() {
			s.mu.Unlock()
			close(done)
		}
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

// TestRecoveredRenderPanicStopsReadingTheScreen pins the containment half of
// the frame fix: once a render panic is recovered the emulator is left alone,
// so a corrupt buffer cannot panic again on the next frame, history or resize
// request and drop the client's connection. A session with no screen stands in
// for that corrupt emulator.
func TestRecoveredRenderPanicStopsReadingTheScreen(t *testing.T) {
	s := &terminalSession{metadata: Terminal{ID: "term_1", Columns: 80, Rows: 24}}
	if err := s.renderOutput([]byte("output")); err == nil {
		t.Fatal("expected the render panic to be recovered into an error")
	}
	if !s.renderBroken {
		t.Fatal("a recovered render panic should mark the screen unusable")
	}

	frame := s.frame()
	if frame.Columns != 80 || frame.Rows != 24 {
		t.Fatalf("fallback frame has the wrong size: %#v", frame)
	}
	if err := s.resize(100, 50); err == nil {
		t.Fatal("resize should refuse a screen that is no longer readable")
	}
}

// Point-in-time reads must fail honestly on that same pane: a blank result
// looks like an empty pane, while the streaming attach path keeps its blank
// frame so a live connection is never dropped for a corrupt screen.
func TestRenderBrokenReadsFailHonestly(t *testing.T) {
	server := NewServer(filepath.Join(t.TempDir(), "socket"))
	s := &terminalSession{metadata: Terminal{ID: "term_1", Columns: 80, Rows: 24}}
	if err := s.renderOutput([]byte("output")); err == nil {
		t.Fatal("expected the render panic to be recovered into an error")
	}
	server.terminals["term_1"] = s

	raw, err := json.Marshal(map[string]string{"terminal_id": "term_1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.terminalRead(raw); err == nil || !strings.Contains(err.Error(), "no longer available") {
		t.Fatalf("terminal.read on a broken screen should fail honestly, got %v", err)
	}
	if _, err := server.terminalHistory(raw); err == nil || !strings.Contains(err.Error(), "no longer available") {
		t.Fatalf("terminal.history on a broken screen should fail honestly, got %v", err)
	}
}
