package tui

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/martintrifunov/orkestar/internal/agent"
	"github.com/martintrifunov/orkestar/internal/agent/claude"
	"github.com/martintrifunov/orkestar/internal/agent/codex"
	"github.com/martintrifunov/orkestar/internal/agent/opencode"
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
	for _, name := range []string{"claude-code", "codex", "opencode"} {
		t.Run(name, func(t *testing.T) { testProgramAdapter(t, name) })
	}
}
func testProgramAdapter(t *testing.T, name string) {
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
	var adapter agent.Adapter
	switch name {
	case "claude-code":
		adapter = claude.New(fixture)
	case "codex":
		adapter = codex.New(fixture)
	case "opencode":
		adapter = opencode.New(fixture, "", nil)
	}
	_, socket := startEmbeddedTestDaemonWithSocket(t, adapter)
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
	// The daemon retains terminal modes and answers queries once across clients.
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

func TestProgramEditsAndReviewsFileWithRealKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "code.txt")
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if b, e := cmd.CombinedOutput(); e != nil {
			t.Fatalf("git: %v %s", e, b)
		}
	}
	git("init")
	git("config", "user.name", "Fixture")
	git("config", "user.email", "fixture@example.invalid")
	os.WriteFile(path, []byte("before\n"), 0644)
	git("add", ".")
	git("commit", "-m", "Initial")
	_, socket := startEmbeddedTestDaemonWithSocket(t)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	process, err := pty.Start(pty.StartOptions{Command: executable, Arguments: []string{"-test.run=^TestTUIProcess$"}, Columns: 150, Rows: 40, Env: append(os.Environ(), "TERM=xterm-256color", "ORKESTAR_TEST_TUI=1", "ORKESTAR_TEST_SOCKET="+socket, "ORKESTAR_TEST_DIRECTORY="+dir, "ORKESTAR_TUI_CONFIG="+filepath.Join(t.TempDir(), "tui.json"))})
	if err != nil {
		t.Fatal(err)
	}
	screen := terminal.NewScreen(150, 40)
	inputDone, outputDone := make(chan struct{}), make(chan struct{})
	go func() { defer close(inputDone); _, _ = io.Copy(process, screen) }()
	go func() { defer close(outputDone); _, _ = io.Copy(screen, process) }()
	defer func() {
		screen.Close()
		process.Close()
		for _, done := range []chan struct{}{inputDone, outputDone} {
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Error("TUI worker did not stop")
			}
		}
	}()
	wait := func(want string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if strings.Contains(screen.Render(), want) {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("missing %q:\n%s", want, screen.Render())
	}
	send := func(text string) {
		t.Helper()
		if _, err := process.Write([]byte(text)); err != nil {
			t.Fatal(err)
		}
	}
	wait("Open a session")
	send("e")
	wait("Open or create")
	send("code.txt\r")
	wait("Ctrl+S save")
	send("\x01edited λ\r\x13")
	deadline := time.Now().Add(5 * time.Second)
	for {
		b, _ := os.ReadFile(path)
		if string(b) == "edited λ\n" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("real keys did not save: %q\n%s", b, screen.Render())
		}
		time.Sleep(10 * time.Millisecond)
	}
	send("\x02d")
	wait("File 1/1")
	wait("edited λ")
	send("\x02o")
	wait("code.txt")
	send("\x02\t")
	send("q")
	select {
	case <-process.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("document TUI did not quit")
	}
}
