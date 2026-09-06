package ptysession_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/martintrifunov/orkestar/internal/agent"
	"github.com/martintrifunov/orkestar/internal/agent/ptysession"
)

// The fixture is written once, before any test forks a child. Writing an
// executable while a sibling parallel test forks lets that child inherit the
// still-open write descriptor, and the exec then fails with ETXTBSY.
var sharedFixture string

func TestMain(m *testing.M) {
	directory, err := os.MkdirTemp("", "orkestar-ptysession")
	if err != nil {
		fmt.Fprintln(os.Stderr, "create fixture directory:", err)
		os.Exit(1)
	}
	sharedFixture = filepath.Join(directory, "fixture-agent")
	if err := os.WriteFile(sharedFixture, []byte("#!/bin/sh\ncat >/dev/null\n"), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "write fixture executable:", err)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(directory)
	os.Exit(code)
}

func fixtureExecutable(t *testing.T) string {
	t.Helper()
	return sharedFixture
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
	var seen []string
	for {
		select {
		case event, ok := <-session.Events():
			if !ok {
				t.Fatalf("events channel closed before reaching state %q, saw %v", want, seen)
			}
			seen = append(seen, fmt.Sprintf("%v(%s)", event.State, event.Reason))
			if event.State == want {
				return
			}
		case <-deadline:
			// Report what did arrive: a wrong terminal state and no state at
			// all need different fixes, and this only fails under CI load.
			t.Fatalf("timed out waiting for state %q; saw %v, session reports %v", want, seen, session.State())
		}
	}
}

// An agent that exits on the interrupt Orkestar sent it has stopped, not
// crashed. The two are read very differently: a crash raises attention, and a
// crash with an unfinished task rings the bell.
//
// Only the ordering makes this visible. Close marks the process as stopping,
// but the interrupt reaches the process first and can kill it before Close is
// ever called, which is what a loaded CI runner does. Linux shows it where
// macOS does not: dash dies of SIGINT, macOS's sh survives it.
func TestExitingOnOurOwnInterruptIsAStop(t *testing.T) {
	t.Parallel()

	session, err := ptysession.Launch("fixture", fixtureExecutable(t), agent.LaunchOptions{
		Directory: t.TempDir(), Columns: 80, Rows: 24,
	})
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	defer session.Close()
	waitForState(t, session, agent.StateReady)

	if err := session.Interrupt(t.Context()); err != nil {
		t.Fatalf("interrupt: %v", err)
	}
	// Stand in for the scheduling delay that lets the interrupt land first.
	time.Sleep(300 * time.Millisecond)
	if err := session.Process().Close(); err != nil {
		t.Fatalf("close process: %v", err)
	}

	waitForState(t, session, agent.StateStopped)
}

// A process that dies of anything else is still a crash: the classification
// has to stay narrow, or it hides the failures it exists to report.
func TestAnUnrequestedExitIsStillACrash(t *testing.T) {
	t.Parallel()

	executable := filepath.Join(t.TempDir(), "fixture")
	if err := os.WriteFile(executable, []byte("#!/bin/sh\nexit 3\n"), 0o755); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	session, err := ptysession.Launch("fixture", executable, agent.LaunchOptions{
		Directory: t.TempDir(), Columns: 80, Rows: 24,
	})
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	defer session.Close()

	waitForState(t, session, agent.StateCrashed)
}

// Ctrl-C reaches an agent two ways, and both are the same request. The sidebar
// i key calls Interrupt; a user typing ^C into an attached pane sends the byte
// as ordinary input, which is the path most people actually use.
func TestTypedControlCCountsAsAnInterrupt(t *testing.T) {
	t.Parallel()

	session, err := ptysession.Launch("fixture", fixtureExecutable(t), agent.LaunchOptions{
		Directory: t.TempDir(), Columns: 80, Rows: 24,
	})
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	defer session.Close()
	waitForState(t, session, agent.StateReady)

	// Written as input, exactly as an attached pane forwards a keystroke.
	if _, err := session.Process().Write([]byte{0x03}); err != nil {
		t.Fatalf("write interrupt: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	if err := session.Process().Close(); err != nil {
		t.Fatalf("close process: %v", err)
	}

	waitForState(t, session, agent.StateStopped)
}
