package agent_test

import (
	"context"
	"testing"
	"time"

	"github.com/martintrifunov/orkestar/internal/agent"
)

func TestFakeAdapterLaunchAndPrompt(t *testing.T) {
	t.Parallel()

	adapter := agent.NewFakeAdapter(agent.Capabilities{
		Name:                "fake",
		SupportsInteractive: true,
		SupportsPrompt:      true,
		SupportsInterrupt:   true,
		SupportsResume:      true,
	})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	session, err := adapter.Launch(ctx, agent.LaunchOptions{Mode: agent.ModeInteractive})
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	defer session.Close()

	if session.NativeSessionID() == "" {
		t.Fatal("expected a native session ID")
	}
	if state := session.State(); state != agent.StateReady {
		t.Fatalf("unexpected initial state: %q", state)
	}

	if err := session.Prompt(ctx, "hello"); err != nil {
		t.Fatalf("prompt: %v", err)
	}
	waitForState(t, session, agent.StateWorking)
	waitForState(t, session, agent.StateReady)

	if err := session.Interrupt(ctx); err != nil {
		t.Fatalf("interrupt: %v", err)
	}
	waitForState(t, session, agent.StateReady)

	if err := session.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if state := session.State(); state != agent.StateStopped {
		t.Fatalf("unexpected state after close: %q", state)
	}
	if _, ok := <-session.Events(); ok {
		t.Fatal("expected events channel to be closed")
	}
}

func TestFakeAdapterRejectsUnsupportedMode(t *testing.T) {
	t.Parallel()

	adapter := agent.NewFakeAdapter(agent.Capabilities{Name: "fake"})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if _, err := adapter.Launch(ctx, agent.LaunchOptions{Mode: agent.ModeInteractive}); err == nil {
		t.Fatal("expected launch to fail for unsupported interactive mode")
	}
	if _, err := adapter.Launch(ctx, agent.LaunchOptions{Mode: agent.ModeManaged}); err == nil {
		t.Fatal("expected launch to fail for unsupported managed mode")
	}
}

func TestFakeAdapterRejectsUnsupportedResume(t *testing.T) {
	t.Parallel()

	adapter := agent.NewFakeAdapter(agent.Capabilities{Name: "fake", SupportsInteractive: true})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if _, err := adapter.Launch(ctx, agent.LaunchOptions{
		Mode:            agent.ModeInteractive,
		ResumeSessionID: "prior-session",
	}); err == nil {
		t.Fatal("expected launch to fail for unsupported resume")
	}
}

func waitForState(t *testing.T, session agent.Session, want agent.State) {
	t.Helper()

	deadline := time.After(time.Second)
	for {
		select {
		case event, ok := <-session.Events():
			if !ok {
				t.Fatalf("events channel closed before reaching state %q", want)
			}
			if event.State == want {
				return
			}
		case <-deadline:
			t.Fatalf("timed out waiting for state %q", want)
		}
	}
}
