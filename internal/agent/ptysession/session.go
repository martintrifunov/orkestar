// Package ptysession provides a reusable agent.Session implementation for
// adapters that run an interactive CLI inside a PTY, the way a human would
// run it in a terminal. internal/agent/claude and internal/agent/opencode
// both use it for their interactive mode; a future adapter for another
// terminal-based agent CLI can reuse it too instead of duplicating the PTY
// lifecycle, prompt/interrupt, and lifecycle-event plumbing.
package ptysession

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os/exec"
	"sync"
	"time"

	"github.com/martintrifunov/orkestar/internal/agent"
	"github.com/martintrifunov/orkestar/internal/pty"
)

// Launch resolves executable on PATH, starts it in a PTY per options, and
// returns a Session wrapping it. idPrefix distinguishes session IDs across
// adapters (e.g. "claude", "opencode") purely for readability in logs and
// snapshots.
func Launch(idPrefix, executable string, options agent.LaunchOptions) (*Session, error) {
	if _, err := exec.LookPath(executable); err != nil {
		return nil, fmt.Errorf("locate %q: %w", executable, err)
	}

	id, err := newID(idPrefix)
	if err != nil {
		return nil, err
	}

	process, err := pty.Start(pty.StartOptions{
		Command:   executable,
		Directory: options.Directory,
		Columns:   options.Columns,
		Rows:      options.Rows,
	})
	if err != nil {
		return nil, fmt.Errorf("start %q: %w", executable, err)
	}

	session := &Session{
		id:      id,
		process: process,
		state:   agent.StateReady,
		events:  make(chan agent.LifecycleEvent, 16),
	}
	session.emit(agent.StateReady, "launched")
	go session.watchExit()
	return session, nil
}

// Session is an interactive agent session backed by a PTY.
type Session struct {
	id      string
	process *pty.Process

	mu     sync.Mutex
	state  agent.State
	closed bool
	events chan agent.LifecycleEvent
}

var (
	_ agent.Session        = (*Session)(nil)
	_ agent.ProcessSession = (*Session)(nil)
)

func (s *Session) ID() string              { return s.id }
func (s *Session) NativeSessionID() string { return "" }

func (s *Session) State() agent.State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// Process returns the underlying PTY process so the daemon can wire this
// session into the existing terminal plumbing (buffer, subscribers, input,
// resize) rather than duplicating it here.
func (s *Session) Process() *pty.Process {
	return s.process
}

// Prompt sends text to the agent as if a user typed it, followed by a
// newline to submit it.
func (s *Session) Prompt(ctx context.Context, text string) error {
	if _, err := s.process.Write([]byte(text + "\n")); err != nil {
		return fmt.Errorf("pty session: send prompt: %w", err)
	}
	return nil
}

// Interrupt sends Ctrl-C (ETX), the same signal an interactive terminal
// user would send to stop the current turn.
func (s *Session) Interrupt(ctx context.Context) error {
	if _, err := s.process.Write([]byte{0x03}); err != nil {
		return fmt.Errorf("pty session: send interrupt: %w", err)
	}
	return nil
}

func (s *Session) Events() <-chan agent.LifecycleEvent {
	return s.events
}

// Close releases the session's event channel. It does not stop the
// underlying PTY process; interactive sessions outlive client detachment.
func (s *Session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	close(s.events)
	return nil
}

func (s *Session) watchExit() {
	waitErr := s.process.WaitError()
	if waitErr != nil {
		s.emit(agent.StateCrashed, waitErr.Error())
	} else {
		s.emit(agent.StateStopped, "process exited")
	}
}

func (s *Session) emit(state agent.State, reason string) {
	s.mu.Lock()
	if s.closed {
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

func newID(prefix string) (string, error) {
	raw := make([]byte, 6)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate session ID: %w", err)
	}
	return prefix + "_" + hex.EncodeToString(raw), nil
}
