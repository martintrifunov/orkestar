package cursor_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/martintrifunov/orkestar/internal/agent"
	"github.com/martintrifunov/orkestar/internal/agent/cursor"
)

// fixtureExecutable stands in for cursor-agent, which is not installed here
// and whose flags have not been verified.
func fixtureExecutable(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cursor-agent")
	if err := os.WriteFile(path, []byte("#!/bin/sh\ncat >/dev/null\n"), 0o755); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

// The capabilities claim only what has been checked. Declaring resume or hook
// support that was guessed at would make the daemon offer a user something
// that quietly does nothing.
func TestCapabilitiesClaimOnlyWhatIsKnown(t *testing.T) {
	capabilities := cursor.New("").Capabilities()
	if capabilities.Name != "cursor" {
		t.Fatalf("unexpected name %q", capabilities.Name)
	}
	if !capabilities.SupportsInteractive || !capabilities.SupportsPrompt || !capabilities.SupportsInterrupt {
		t.Fatalf("unexpected capabilities: %#v", capabilities)
	}
	if capabilities.SupportsManaged || capabilities.SupportsResume {
		t.Fatalf("claims support that has not been verified: %#v", capabilities)
	}
}

func TestLaunchesInteractively(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	session, err := cursor.New(fixtureExecutable(t)).Launch(ctx, agent.LaunchOptions{
		Mode: agent.ModeInteractive, Directory: t.TempDir(), Columns: 80, Rows: 24,
	})
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	defer session.Close()
	if _, ok := session.(agent.ProcessSession); !ok {
		t.Fatal("an interactive session should own a terminal")
	}
}

// A resume that cannot be honoured is refused rather than quietly starting a
// fresh session, which would lose whatever the old one held.
func TestResumeIsRefusedRatherThanIgnored(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if _, err := cursor.New(fixtureExecutable(t)).Launch(ctx, agent.LaunchOptions{
		Mode: agent.ModeInteractive, ResumeSessionID: "prior",
	}); err == nil {
		t.Fatal("resume was accepted")
	}
	if _, err := cursor.New(fixtureExecutable(t)).Launch(ctx, agent.LaunchOptions{
		Mode: agent.ModeManaged,
	}); err == nil {
		t.Fatal("managed mode was accepted")
	}
}
