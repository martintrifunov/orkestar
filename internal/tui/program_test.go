package tui

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/martintrifunov/orkestar/internal/agent/claude"
	"github.com/martintrifunov/orkestar/internal/ipc"
	"github.com/martintrifunov/orkestar/internal/pty"
	"github.com/martintrifunov/orkestar/internal/terminal"
)

func TestTUIProcess(t *testing.T) {
	if os.Getenv("ORKESTAR_TEST_TUI") != "1" {
		t.Skip("subprocess helper")
	}
	if err := Run(ipc.NewClient(os.Getenv("ORKESTAR_TEST_SOCKET")), os.Getenv("ORKESTAR_TEST_DIRECTORY")); err != nil {
		t.Fatal(err)
	}
}

// Drive Bubble Tea's real input decoder and renderer inside a real outer PTY,
// including closing/reopening the client while its agent remains in the daemon.
func TestProgramEmbedsAgentAndReattaches(t *testing.T) {
	dir := t.TempDir()
	fixture := filepath.Join(dir, "claude")
	script := `#!/bin/sh
stty -echo -icanon
printf '\033[H\033[6n'
dd bs=1 count=6 >/dev/null 2>&1
printf 'fixture-ready\r\n'
while IFS= read -r line; do printf 'received:%s\r\n' "$line"; done
`
	if err := os.WriteFile(fixture, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	_, socket := startEmbeddedTestDaemonWithSocket(t, claude.New(fixture))
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	start := func() (*pty.Process, *terminal.Screen) {
		process, err := pty.Start(pty.StartOptions{
			Command: executable, Arguments: []string{"-test.run=^TestTUIProcess$"},
			Columns: 120, Rows: 40,
			Env: append(os.Environ(), "TERM=xterm-256color", "ORKESTAR_TEST_TUI=1", "ORKESTAR_TEST_SOCKET="+socket, "ORKESTAR_TEST_DIRECTORY="+dir),
		})
		if err != nil {
			t.Fatal(err)
		}
		screen := terminal.NewScreen(120, 40)
		outputDone, inputDone := make(chan struct{}), make(chan struct{})
		go func() { defer close(inputDone); _, _ = io.Copy(process, screen) }()
		go func() { defer close(outputDone); _, _ = io.Copy(screen, process) }()
		t.Cleanup(func() {
			_ = screen.Close()
			_ = process.Close()
			for _, done := range []<-chan struct{}{outputDone, inputDone, process.Done()} {
				select {
				case <-done:
				case <-time.After(3 * time.Second):
					t.Error("TUI subprocess did not stop")
				}
			}
		})
		return process, screen
	}
	wait := func(screen *terminal.Screen, want string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if strings.Contains(screen.Render(), want) {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("TUI did not show %q:\n%s", want, screen.Render())
	}
	process, screen := start()
	wait(screen, "Open a session")
	_, _ = process.Write([]byte("a"))
	wait(screen, "New agent")
	_, _ = process.Write([]byte("\r"))
	wait(screen, "fixture-ready")
	_, _ = process.Write([]byte("hello123\r"))
	wait(screen, "received:hello123")
	for _, heading := range []string{"Workspaces", "Sessions", "Tasks", "Agents"} {
		wait(screen, heading)
	}
	_, _ = process.Write([]byte("\x02\t"))
	wait(screen, "esc terminal")
	_, _ = process.Write([]byte("q"))
	select {
	case <-process.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("sidebar quit froze")
	}
	process, screen = start()
	wait(screen, "Open a session")
	_, _ = process.Write([]byte("\r"))
	wait(screen, "received:hello123")
	_, _ = process.Write([]byte("still-running\r"))
	wait(screen, "received:")
	// Replayed terminal queries can produce an extra cursor reply in raw input;
	// the stable marker proves the existing process still accepts subsequent keys.
	wait(screen, "still-running")
	_, _ = process.Write([]byte("\x02\t"))
	wait(screen, "esc terminal")
	_, _ = process.Write([]byte("q"))
	select {
	case <-process.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("reopened TUI did not quit")
	}
}
