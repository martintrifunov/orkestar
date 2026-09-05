package daemon_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/martintrifunov/orkestar/internal/agent/claude"
	"github.com/martintrifunov/orkestar/internal/daemon"
	"github.com/martintrifunov/orkestar/internal/ipc"
)

// fixtureAgentExecutable writes a shell script that echoes back whatever it
// reads, prefixed, so a test can prove terminal I/O actually reaches the
// bridged agent process without depending on a real Claude Code install.
func fixtureAgentExecutable(t *testing.T) string {
	t.Helper()

	directory := t.TempDir()
	path := filepath.Join(directory, "claude")
	script := "#!/bin/sh\nprintf 'ready\\n'\nwhile IFS= read -r line; do printf 'echo:%s\\n' \"$line\"; done\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fixture executable: %v", err)
	}
	return path
}

func TestLaunchInteractiveAgentBridgesToAttachableTerminal(t *testing.T) {
	t.Parallel()

	temporaryDirectory := t.TempDir()
	socketDirectory, err := os.MkdirTemp("/tmp", "orkestar-agent-bridge-test-")
	if err != nil {
		t.Fatalf("create socket directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDirectory) })
	socketPath := filepath.Join(socketDirectory, "orkestar.sock")

	server := daemon.NewServer(socketPath)
	server.RegisterAdapter(claude.New(fixtureAgentExecutable(t)))

	ctx, cancel := context.WithCancel(context.Background())
	serverError := make(chan error, 1)
	go func() { serverError <- server.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-serverError; err != nil {
			t.Errorf("server shutdown: %v", err)
		}
	})

	client := ipc.NewClient(socketPath)
	waitForServer(t, client)
	callContext, callCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer callCancel()

	var workspace daemon.Workspace
	if err := client.Call(callContext, "workspace.create", map[string]string{
		"directory": temporaryDirectory,
	}, &workspace); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	var launched daemon.Agent
	if err := client.Call(callContext, "agent.launch", map[string]any{
		"workspace_id": workspace.ID,
		"adapter":      "claude-code",
		"mode":         "interactive",
		"columns":      80,
		"rows":         24,
	}, &launched); err != nil {
		t.Fatalf("launch agent: %v", err)
	}
	if launched.TerminalID == "" {
		t.Fatalf("expected an interactive agent to have a bridged terminal ID: %#v", launched)
	}

	// The bridged terminal must be independently visible and attachable,
	// exactly like any other terminal.
	var snapshot daemon.Snapshot
	if err := client.Call(callContext, "system.snapshot", nil, &snapshot); err != nil {
		t.Fatalf("get snapshot: %v", err)
	}
	found := false
	for _, terminal := range snapshot.Terminals {
		if terminal.ID == launched.TerminalID {
			found = true
			if terminal.WorkspaceID != workspace.ID {
				t.Fatalf("unexpected bridged terminal workspace: %#v", terminal)
			}
		}
	}
	if !found {
		t.Fatalf("expected bridged terminal %q in snapshot.Terminals: %#v", launched.TerminalID, snapshot.Terminals)
	}

	// The fixture prints as soon as it starts, so that output can already be on
	// the daemon's screen when the attach completes. Search the replay the
	// attach returns as well as the events that follow it; watching only the
	// events makes the test a race the launch usually wins on Linux.
	stream, replay := openTerminal(t, callContext, client, launched.TerminalID)
	defer stream.Close()

	readUntil(t, stream, replay, []byte("ready"))

	// Prompting the agent must produce output on the SAME terminal stream,
	// proving prompt and terminal attach share one underlying PTY.
	if err := client.Call(callContext, "agent.prompt", map[string]string{
		"agent_id": launched.ID,
		"text":     "hello",
	}, new(map[string]string)); err != nil {
		t.Fatalf("prompt agent: %v", err)
	}
	readUntil(t, stream, nil, []byte("echo:hello"))
}

func TestSnapshotListsRegisteredAdapters(t *testing.T) {
	t.Parallel()

	socketDirectory, err := os.MkdirTemp("/tmp", "orkestar-adapters-test-")
	if err != nil {
		t.Fatalf("create socket directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDirectory) })
	socketPath := filepath.Join(socketDirectory, "orkestar.sock")

	server := daemon.NewServer(socketPath)
	server.RegisterAdapter(claude.New(fixtureAgentExecutable(t)))

	ctx, cancel := context.WithCancel(context.Background())
	serverError := make(chan error, 1)
	go func() { serverError <- server.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-serverError; err != nil {
			t.Errorf("server shutdown: %v", err)
		}
	})

	client := ipc.NewClient(socketPath)
	waitForServer(t, client)
	callContext, callCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer callCancel()

	var snapshot daemon.Snapshot
	if err := client.Call(callContext, "system.snapshot", nil, &snapshot); err != nil {
		t.Fatalf("get snapshot: %v", err)
	}
	if len(snapshot.Adapters) != 1 || snapshot.Adapters[0].Name != "claude-code" {
		t.Fatalf("unexpected adapters in snapshot: %#v", snapshot.Adapters)
	}
	if !snapshot.Adapters[0].SupportsInteractive {
		t.Fatalf("expected claude-code to report interactive support: %#v", snapshot.Adapters[0])
	}
}
