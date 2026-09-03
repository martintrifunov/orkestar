// Package claude implements the Claude Code interactive agent adapter. It
// launches the installed `claude` executable inside a PTY and adapts it to
// the internal/agent contracts; there is no managed/SDK mode yet.
package claude

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"sync"
	"time"

	"github.com/martintrifunov/orkestar/internal/agent"
	"github.com/martintrifunov/orkestar/internal/pty"
)

const defaultExecutable = "claude"

// Adapter launches Claude Code interactively in a PTY.
type Adapter struct {
	executable string
}

// New returns an Adapter that runs the given executable. An empty
// executable defaults to "claude" resolved from PATH. Tests can point this
// at a fixture script instead of a real Claude Code installation.
func New(executable string) *Adapter {
	if executable == "" {
		executable = defaultExecutable
	}
	return &Adapter{executable: executable}
}

func (a *Adapter) Capabilities() agent.Capabilities {
	return agent.Capabilities{
		Name:                "claude-code",
		SupportsInteractive: true,
		SupportsManaged:     false,
		SupportsPrompt:      true,
		SupportsInterrupt:   true,
		SupportsResume:      false,
	}
}

func (a *Adapter) Launch(ctx context.Context, options agent.LaunchOptions) (agent.Session, error) {
	if options.Mode != agent.ModeInteractive {
		return nil, fmt.Errorf("claude adapter: mode %q is not supported", options.Mode)
	}
	if options.ResumeSessionID != "" {
		return nil, errors.New("claude adapter: resume is not supported yet")
	}
	if _, err := exec.LookPath(a.executable); err != nil {
		return nil, fmt.Errorf("claude adapter: locate %q: %w", a.executable, err)
	}

	id, err := newID()
	if err != nil {
		return nil, fmt.Errorf("claude adapter: %w", err)
	}

	process, err := pty.Start(pty.StartOptions{
		Command:   a.executable,
		Directory: options.Directory,
		Columns:   options.Columns,
		Rows:      options.Rows,
	})
	if err != nil {
		return nil, fmt.Errorf("claude adapter: start %q: %w", a.executable, err)
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

// Session is an interactive Claude Code agent session backed by a PTY.
type Session struct {
	id      string
	process *pty.Process

	mu     sync.Mutex
	state  agent.State
	closed bool
	events chan agent.LifecycleEvent
}

var _ agent.Session = (*Session)(nil)

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

// Prompt sends text to Claude Code as if a user typed it, followed by a
// newline to submit it.
func (s *Session) Prompt(ctx context.Context, text string) error {
	if _, err := s.process.Write([]byte(text + "\n")); err != nil {
		return fmt.Errorf("claude session: send prompt: %w", err)
	}
	return nil
}

// Interrupt sends Ctrl-C (ETX), the same signal an interactive terminal
// user would send to stop the current turn.
func (s *Session) Interrupt(ctx context.Context) error {
	if _, err := s.process.Write([]byte{0x03}); err != nil {
		return fmt.Errorf("claude session: send interrupt: %w", err)
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

func newID() (string, error) {
	raw := make([]byte, 6)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate session ID: %w", err)
	}
	return "claude_" + hex.EncodeToString(raw), nil
}
