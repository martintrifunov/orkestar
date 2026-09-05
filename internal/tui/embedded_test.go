package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/martintrifunov/orkestar/internal/agent"
	"github.com/martintrifunov/orkestar/internal/agent/claude"
	"github.com/martintrifunov/orkestar/internal/daemon"
	"github.com/martintrifunov/orkestar/internal/ipc"
)

func startEmbeddedTestDaemon(t *testing.T, adapters ...agent.Adapter) *ipc.Client {
	client, _ := startEmbeddedTestDaemonWithSocket(t, adapters...)
	return client
}

func startEmbeddedTestDaemonWithSocket(t *testing.T, adapters ...agent.Adapter) (*ipc.Client, string) {
	t.Helper()

	socketDirectory, err := os.MkdirTemp("/tmp", "orkestar-embedded-test-")
	if err != nil {
		t.Fatalf("create socket directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDirectory) })
	socketPath := filepath.Join(socketDirectory, "orkestar.sock")

	server := daemon.NewServer(socketPath)
	for _, adapter := range adapters {
		server.RegisterAdapter(adapter)
	}
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
			return client, socketPath
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("daemon did not become ready")
	return nil, ""
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
		var msg tea.Msg
		select {
		case msg = <-term.events:
		case <-time.After(time.Until(deadline)):
			t.Fatalf("timed out waiting for %q", want)
		}
		event, ok := msg.(embeddedEventMsg)
		if !ok {
			t.Fatalf("unexpected message type from waitEmbeddedEvent: %#v", msg)
		}
		if strings.Contains(term.emulator.Render(), want) {
			return
		}
		if event.exited {
			t.Fatalf("terminal exited before seeing %q: %v", want, event.err)
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
	defer ready.terminal.close()

	waitForEmbeddedEvent(t, ready.terminal, "embedded ready")

	// Sending input must reach the real process and its reply must show
	// up rendered in the SAME emulator, proving the pane and the process
	// share one underlying PTY end to end.
	sendEmbeddedInput(ready.terminal.stream, []byte("hello\n"))
	waitForEmbeddedEvent(t, ready.terminal, "echo:hello")
}

// TestUpdateEmbeddedPreservesKeystrokeOrder exercises the real Update path
// a running app uses (Model.updateEmbedded), typing a sequence of distinct
// keys and confirming the process receives them in the order they were
// pressed. Sending was originally wrapped in a tea.Cmd per keystroke;
// Bubble Tea gives no ordering guarantee across concurrently-scheduled Cmd
// goroutines, so fast typing could have reached the daemon scrambled. This
// guards against that regression by calling updateEmbedded directly, the
// same way Update's tea.KeyPressMsg case does.
func TestUpdateEmbeddedPreservesKeystrokeOrder(t *testing.T) {
	t.Parallel()

	client := startEmbeddedTestDaemon(t)
	started := startEmbeddedTestTerminal(t, client, []string{
		"/bin/sh", "-c", "printf 'ready\\n'; IFS= read -r line; printf 'echo:%s\\n' \"$line\"",
	})

	msg := openEmbeddedTerminal(client, started.ID, 80, 24)()
	ready, ok := msg.(embeddedReadyMsg)
	if !ok || ready.err != nil {
		t.Fatalf("open embedded terminal: msg=%#v", msg)
	}
	defer ready.terminal.close()
	waitForEmbeddedEvent(t, ready.terminal, "ready")

	model := Model{embedded: ready.terminal}
	for _, r := range "hello" {
		model.updateEmbedded(tea.KeyPressMsg{Text: string(r), Code: r})
	}
	model.updateEmbedded(tea.KeyPressMsg{Code: tea.KeyEnter})

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
	defer ready.terminal.close()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var msg tea.Msg
		select {
		case msg = <-ready.terminal.events:
		case <-time.After(time.Until(deadline)):
			t.Fatal("timed out waiting for exit")
		}
		event, ok := msg.(embeddedEventMsg)
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

func openTestPane(t *testing.T, cmd tea.Cmd) *embeddedTerminal {
	t.Helper()
	result := make(chan tea.Msg, 1)
	go func() { result <- cmd() }()
	select {
	case msg := <-result:
		ready, ok := msg.(embeddedReadyMsg)
		if !ok || ready.err != nil {
			t.Fatalf("open pane: %#v", msg)
		}
		t.Cleanup(ready.terminal.close)
		return ready.terminal
	case <-time.After(3 * time.Second):
		t.Fatal("opening pane blocked on terminal replay")
	}
	return nil
}

func TestAgentPaneAnswersQueriesAndKeepsAcceptingKeys(t *testing.T) {
	// A real bridged agent PTY with capability probes at startup and after
	// the first submitted line reproduces the rich-CLI freeze without Claude.
	path := filepath.Join(t.TempDir(), "fixture-agent")
	script := `#!/bin/sh
stty -echo -icanon
probe() {
 printf '\033[H\033[6n'
 reply=$(dd bs=1 count=6 2>/dev/null)
 expected=$(printf '\033[1;1R')
 [ "$reply" = "$expected" ] || exit 2
}
probe
printf 'query-ready\r\n'
IFS= read -r line
printf 'first:%s\r\n' "$line"
probe
printf 'query-again\r\n'
IFS= read -r line
printf 'second:%s\r\n' "$line"
while IFS= read -r line; do printf 'echo:%s\r\n' "$line"; done
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	adapter := claude.New(path)
	client := startEmbeddedTestDaemon(t, adapter)
	m := New(client, t.TempDir())
	m.width, m.height = 120, 35
	m.snapshot.Adapters = []agent.Capabilities{adapter.Capabilities()}
	launched := m.launchPickedAgent()().(agentLaunchedMsg)
	if launched.err != nil {
		t.Fatal(launched.err)
	}
	updated, cmd := m.Update(launched)
	m = updated.(Model)
	term := openTestPane(t, cmd)
	updated, _ = m.Update(embeddedReadyMsg{terminal: term})
	m = updated.(Model)
	waitForEmbeddedEvent(t, term, "query-ready")
	for _, word := range []string{"hello", "123456789"} {
		for _, r := range word {
			updated, _ = m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
			m = updated.(Model)
		}
		updated, _ = m.Update(specialKey(tea.KeyEnter))
		m = updated.(Model)
		want := "query-again"
		if word == "123456789" {
			want = "second:123456789"
		}
		waitForEmbeddedEvent(t, term, want)
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	m = updated.(Model)
	updated, _ = m.Update(specialKey(tea.KeyTab))
	m = updated.(Model)
	if !m.sidebarFocused || m.embedded != term {
		t.Fatal("sidebar focus lost live pane")
	}
	for _, heading := range []string{"Sessions", "Agents", "Tasks", "Workspaces", "second:123456789"} {
		if !strings.Contains(m.render(), heading) {
			t.Fatalf("pane layout missing %q", heading)
		}
	}
}

func TestPaneReconnectResizeAndCancellation(t *testing.T) {
	client := startEmbeddedTestDaemon(t)
	started := startEmbeddedTestTerminal(t, client, []string{"/bin/sh", "-c", "printf 'ready\\n'; while IFS= read -r line; do printf 'echo:%s\\n' \"$line\"; stty size; done"})
	first := openTestPane(t, openEmbeddedTerminal(client, started.ID, 80, 24))
	waitForEmbeddedEvent(t, first, "ready")
	first.close()
	for _, done := range []chan struct{}{first.readDone, first.writeDone} {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("detached pane leaked goroutine")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	second := openTestPane(t, openEmbeddedTerminalContext(ctx, client, started.ID, 60, 18))
	sendEmbeddedResize(second, 70, 20)
	second.emulator.Input([]byte("reattached\n"))
	waitForEmbeddedEvent(t, second, "20 70")
	if !strings.Contains(second.emulator.Render(), "echo:reattached") {
		t.Fatal("reattachment lost input")
	}
	cancel()
	for _, done := range []chan struct{}{second.readDone, second.writeDone} {
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("cancellation leaked goroutine")
		}
	}
}
