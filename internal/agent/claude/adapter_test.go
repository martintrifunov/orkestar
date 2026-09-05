package claude_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/martintrifunov/orkestar/internal/agent"
	"github.com/martintrifunov/orkestar/internal/agent/claude"
)

func TestAdapterCapabilities(t *testing.T) {
	t.Parallel()

	capabilities := claude.New("").Capabilities()
	if capabilities.Name != "claude-code" {
		t.Fatalf("unexpected adapter name: %q", capabilities.Name)
	}
	if !capabilities.SupportsInteractive || !capabilities.SupportsPrompt || !capabilities.SupportsInterrupt {
		t.Fatalf("unexpected capabilities: %#v", capabilities)
	}
	if capabilities.SupportsManaged || !capabilities.SupportsResume {
		t.Fatalf("unexpected capabilities: %#v", capabilities)
	}
}

func TestAdapterRejectsManagedMode(t *testing.T) {
	t.Parallel()

	adapter := claude.New(fixtureExecutable(t))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := adapter.Launch(ctx, agent.LaunchOptions{Mode: agent.ModeManaged}); err == nil {
		t.Fatal("expected managed mode to be rejected")
	}
}

func TestAdapterAcceptsResume(t *testing.T) {
	t.Parallel()

	adapter := claude.New(fixtureExecutable(t))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	session, err := adapter.Launch(ctx, agent.LaunchOptions{Mode: agent.ModeInteractive, ResumeSessionID: "prior"})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if session.NativeSessionID() != "prior" {
		t.Fatal("resume identity not retained")
	}

}

func TestAdapterLaunchPromptAndInterrupt(t *testing.T) {
	t.Parallel()

	adapter := claude.New(fixtureExecutable(t))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	session, err := adapter.Launch(ctx, agent.LaunchOptions{
		Mode:      agent.ModeInteractive,
		Directory: t.TempDir(),
		Columns:   80,
		Rows:      24,
	})
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	defer session.Close()

	waitForState(t, session, agent.StateReady)

	if err := session.Prompt(ctx, "hello"); err != nil {
		t.Fatalf("prompt: %v", err)
	}
	if err := session.Interrupt(ctx); err != nil {
		t.Fatalf("interrupt: %v", err)
	}

	processSession, ok := session.(agent.ProcessSession)
	if !ok {
		t.Fatal("expected session to implement agent.ProcessSession")
	}
	if err := processSession.Process().Close(); err != nil {
		t.Fatalf("close process: %v", err)
	}

	waitForState(t, session, agent.StateStopped)
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

// fixtureExecutable writes a small shell script that behaves enough like an
// interactive CLI (reads stdin, ignores it, exits when its PTY closes) to
// exercise the adapter without depending on a real Claude Code install.
func fixtureExecutable(t *testing.T) string {
	t.Helper()

	directory := t.TempDir()
	path := filepath.Join(directory, "claude")
	script := "#!/bin/sh\ncat >/dev/null\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fixture executable: %v", err)
	}
	return path
}
