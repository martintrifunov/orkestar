package agent

import (
	"context"
	"errors"
	"sync"
	"time"
)

// FakeAdapter is a minimal in-memory Adapter for tests. It never spawns a
// real process, so it is safe to use without Claude Code, OpenCode, or any
// other agent installed.
type FakeAdapter struct {
	capabilities Capabilities
}

// NewFakeAdapter returns a FakeAdapter reporting the given capabilities.
func NewFakeAdapter(capabilities Capabilities) *FakeAdapter {
	return &FakeAdapter{capabilities: capabilities}
}

func (a *FakeAdapter) Capabilities() Capabilities {
	return a.capabilities
}

func (a *FakeAdapter) Launch(ctx context.Context, options LaunchOptions) (Session, error) {
	if options.Mode == ModeInteractive && !a.capabilities.SupportsInteractive {
		return nil, errors.New("fake adapter: interactive mode not supported")
	}
	if options.Mode == ModeManaged && !a.capabilities.SupportsManaged {
		return nil, errors.New("fake adapter: managed mode not supported")
	}
	if options.ResumeSessionID != "" && !a.capabilities.SupportsResume {
		return nil, errors.New("fake adapter: resume not supported")
	}

	session := &fakeSession{
		id:         "fake-session",
		nativeID:   options.ResumeSessionID,
		state:      StateReady,
		events:     make(chan LifecycleEvent, 16),
		capability: a.capabilities,
	}
	if session.nativeID == "" {
		session.nativeID = "fake-native-session"
	}
	session.emit(StateReady, "launched")
	return session, nil
}

type fakeSession struct {
	id         string
	nativeID   string
	capability Capabilities

	mu     sync.Mutex
	state  State
	closed bool
	events chan LifecycleEvent
}

func (s *fakeSession) ID() string                    { return s.id }
func (s *fakeSession) NativeSessionID() string       { return s.nativeID }
func (s *fakeSession) Events() <-chan LifecycleEvent { return s.events }

func (s *fakeSession) State() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

func (s *fakeSession) Prompt(ctx context.Context, text string) error {
	if !s.capability.SupportsPrompt {
		return errors.New("fake adapter: prompt not supported")
	}
	s.emit(StateWorking, "prompted")
	s.emit(StateReady, "prompted")
	return nil
}

func (s *fakeSession) Interrupt(ctx context.Context) error {
	if !s.capability.SupportsInterrupt {
		return errors.New("fake adapter: interrupt not supported")
	}
	s.emit(StateReady, "interrupted")
	return nil
}

func (s *fakeSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.state = StateStopped
	close(s.events)
	return nil
}

func (s *fakeSession) emit(state State, reason string) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.state = state
	s.mu.Unlock()

	event := LifecycleEvent{State: state, Reason: reason, Timestamp: time.Now().UTC()}
	select {
	case s.events <- event:
	default:
	}
}
