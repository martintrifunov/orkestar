package ptysession_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/martintrifunov/orkestar/internal/agent"
	"github.com/martintrifunov/orkestar/internal/agent/ptysession"
)

func fixtureExecutable(t *testing.T) string {
	t.Helper()

	directory := t.TempDir()
	path := filepath.Join(directory, "fixture-agent")
	script := "#!/bin/sh\ncat >/dev/null\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fixture executable: %v", err)
	}
	return path
}

func TestLaunchRejectsMissingExecutable(t *testing.T) {
	t.Parallel()

	if _, err := ptysession.Launch("fixture", "does-not-exist-on-path", agent.LaunchOptions{}); err == nil {
		t.Fatal("expected launch to fail for a missing executable")
	}
}

func TestLaunchIDHasPrefix(t *testing.T) {
	t.Parallel()

	session, err := ptysession.Launch("fixture", fixtureExecutable(t), agent.LaunchOptions{
		Directory: t.TempDir(), Columns: 80, Rows: 24,
	})
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	defer session.Close()

	if got := session.ID(); len(got) < len("fixture_") || got[:len("fixture_")] != "fixture_" {
		t.Fatalf("expected session ID to start with %q, got %q", "fixture_", got)
	}
	if session.NativeSessionID() != "" {
		t.Fatalf("expected no native session ID, got %q", session.NativeSessionID())
	}
}

func TestLaunchPromptInterruptAndExit(t *testing.T) {
	t.Parallel()

	session, err := ptysession.Launch("fixture", fixtureExecutable(t), agent.LaunchOptions{
		Directory: t.TempDir(), Columns: 80, Rows: 24,
	})
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	defer session.Close()

	waitForState(t, session, agent.StateReady)

	if err := session.Prompt(t.Context(), "hello"); err != nil {
		t.Fatalf("prompt: %v", err)
	}
	if err := session.Interrupt(t.Context()); err != nil {
		t.Fatalf("interrupt: %v", err)
	}

	if err := session.Process().Close(); err != nil {
		t.Fatalf("close process: %v", err)
	}
	waitForState(t, session, agent.StateStopped)
}

func TestCloseIsIdempotentAndClosesEvents(t *testing.T) {
	t.Parallel()

	session, err := ptysession.Launch("fixture", fixtureExecutable(t), agent.LaunchOptions{Directory: t.TempDir()})
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	// A closed buffered channel still yields any already-queued events
	// before reporting closed, so drain it rather than checking once.
	for {
		if _, ok := <-session.Events(); !ok {
			break
		}
	}
}

func waitForState(t *testing.T, session agent.Session, want agent.State) {
	t.Helper()

	deadline := time.After(5 * time.Second)
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
