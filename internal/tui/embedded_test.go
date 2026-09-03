package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/martintrifunov/orkestar/internal/daemon"
	"github.com/martintrifunov/orkestar/internal/ipc"
)

func startEmbeddedTestDaemon(t *testing.T) *ipc.Client {
	t.Helper()

	socketDirectory, err := os.MkdirTemp("/tmp", "orkestar-embedded-test-")
	if err != nil {
		t.Fatalf("create socket directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDirectory) })
	socketPath := filepath.Join(socketDirectory, "orkestar.sock")

	server := daemon.NewServer(socketPath)
	ctx, cancel := context.WithCancel(context.Background())
	serverError := make(chan error, 1)
	go func() { serverError <- server.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-serverError; err != nil {
			t.Errorf("daemon shutdown: %v", err)
		}
	})

	client := ipc.NewClient(socketPath)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		pingCtx, pingCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		var result map[string]string
		err := client.Call(pingCtx, "system.ping", nil, &result)
		pingCancel()
		if err == nil {
			return client
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("daemon did not become ready")
	return nil
}

func startEmbeddedTestTerminal(t *testing.T, client *ipc.Client, command []string) daemon.Terminal {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	var workspace daemon.Workspace
	if err := client.Call(ctx, "workspace.create", map[string]string{
		"directory": t.TempDir(),
	}, &workspace); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	var started daemon.Terminal
	if err := client.Call(ctx, "terminal.start", map[string]any{
		"workspace_id": workspace.ID,
		"command":      command,
		"columns":      80,
		"rows":         24,
	}, &started); err != nil {
		t.Fatalf("start terminal: %v", err)
	}
	return started
}

// waitForEmbeddedEvent pulls events off term.events (running
// waitEmbeddedEvent's Cmd synchronously) until the emulator's rendered
// content contains want, or the deadline passes.
//
// It reads the emulator with Render, not String: SafeEmulator wraps Write,
// Render, and Resize with locking, but String falls through unprotected
// via struct embedding, which races against the reader goroutine's Write
// calls. Render is what production code (renderEmbedded) uses too.
func waitForEmbeddedEvent(t *testing.T, term *embeddedTerminal, want string) {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		msg := waitEmbeddedEvent(term.events)()
		event, ok := msg.(embeddedEventMsg)
		if !ok {
			t.Fatalf("unexpected message type from waitEmbeddedEvent: %#v", msg)
		}
		if event.exited {
			t.Fatalf("terminal exited before seeing %q", want)
		}
		if strings.Contains(term.emulator.Render(), want) {
			return
		}
	}
	t.Fatalf("timed out waiting for embedded terminal to show %q; last content:\n%s", want, term.emulator.Render())
}

func TestOpenEmbeddedTerminalRendersRealOutput(t *testing.T) {
	t.Parallel()

	client := startEmbeddedTestDaemon(t)
	started := startEmbeddedTestTerminal(t, client, []string{
		"/bin/sh", "-c", "printf 'embedded ready\\n'; IFS= read -r line; printf 'echo:%s\\n' \"$line\"",
	})

	msg := openEmbeddedTerminal(client, started.ID, 80, 24)()
	ready, ok := msg.(embeddedReadyMsg)
	if !ok {
		t.Fatalf("unexpected message type: %#v", msg)
	}
	if ready.err != nil {
		t.Fatalf("open embedded terminal: %v", ready.err)
	}
	defer ready.terminal.stream.Close()

	waitForEmbeddedEvent(t, ready.terminal, "embedded ready")

	// Sending input must reach the real process and its reply must show
	// up rendered in the SAME emulator, proving the pane and the process
	// share one underlying PTY end to end.
	sendEmbeddedInput(ready.terminal.stream, []byte("hello\n"))()
	waitForEmbeddedEvent(t, ready.terminal, "echo:hello")
}

func TestOpenEmbeddedTerminalReportsExit(t *testing.T) {
	t.Parallel()

	client := startEmbeddedTestDaemon(t)
	started := startEmbeddedTestTerminal(t, client, []string{"/bin/sh", "-c", "exit 0"})

	msg := openEmbeddedTerminal(client, started.ID, 80, 24)()
	ready, ok := msg.(embeddedReadyMsg)
	if !ok || ready.err != nil {
		t.Fatalf("open embedded terminal: msg=%#v", msg)
	}
	defer ready.terminal.stream.Close()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		event, ok := waitEmbeddedEvent(ready.terminal.events)().(embeddedEventMsg)
		if !ok {
			t.Fatalf("unexpected message type from waitEmbeddedEvent")
		}
		if event.exited {
			return
		}
	}
	t.Fatal("timed out waiting for terminal exit event")
}

func TestOpenEmbeddedTerminalRejectsUnknownID(t *testing.T) {
	t.Parallel()

	client := startEmbeddedTestDaemon(t)
	msg := openEmbeddedTerminal(client, "does-not-exist", 80, 24)()
	ready, ok := msg.(embeddedReadyMsg)
	if !ok {
		t.Fatalf("unexpected message type: %#v", msg)
	}
	if ready.err == nil {
		t.Fatal("expected an error for an unknown terminal ID")
	}
}

func TestEmbeddedPaneSizeClamps(t *testing.T) {
	t.Parallel()

	if columns, rows := embeddedPaneSize(10, 10); columns < 20 || rows < 5 {
		t.Fatalf("expected pane size to clamp to minimums, got columns=%d rows=%d", columns, rows)
	}
	columns, rows := embeddedPaneSize(200, 60)
	if columns <= 0 || rows <= 0 {
		t.Fatalf("expected positive pane size for a large window, got columns=%d rows=%d", columns, rows)
	}
}

func TestEmbeddedSidebarWidthClamps(t *testing.T) {
	t.Parallel()

	if got := embeddedSidebarWidth(30); got < 24 {
		t.Fatalf("expected sidebar width to clamp to a minimum of 24, got %d", got)
	}
	if got := embeddedSidebarWidth(300); got > 40 {
		t.Fatalf("expected sidebar width to clamp to a maximum of 40, got %d", got)
	}
}
