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
	path := filepath.Join(dir, "code.go")
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
	send("code.go\r")
	wait("Ctrl+S save")
	// The status line naming the language proves the background lexer ran and
	// delivered its result through the real program loop.
	wait("· Go")
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
	wait("code.go")
	send("\x02\t")
	send("q")
	select {
	case <-process.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("document TUI did not quit")
	}
}

// Drive real Ctrl+b v/s keystrokes through Bubble Tea's decoder in an outer
// PTY and check that each split produces another visible shell pane, that a
// stacked split followed by a side-by-side split nests rather than rearranges,
// and that closing one pane leaves the others running.
func TestProgramNestedSplitsWithRealKeys(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	dir := t.TempDir()
	_, socket := startEmbeddedTestDaemonWithSocket(t)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	process, err := pty.Start(pty.StartOptions{
		Command: executable, Arguments: []string{"-test.run=^TestTUIProcess$"},
		Columns: 170, Rows: 48,
		Env: append(os.Environ(), "TERM=xterm-256color", "SHELL=/bin/sh", "ORKESTAR_TEST_TUI=1",
			"ORKESTAR_TEST_SOCKET="+socket, "ORKESTAR_TEST_DIRECTORY="+dir,
			"ORKESTAR_TUI_CONFIG="+filepath.Join(t.TempDir(), "tui.json"), "PS1=$ "),
	})
	if err != nil {
		t.Fatal(err)
	}
	screen := terminal.NewScreen(170, 48)
	inputDone, outputDone := make(chan struct{}), make(chan struct{})
	go func() { defer close(inputDone); _, _ = io.Copy(process, screen) }()
	go func() { defer close(outputDone); _, _ = io.Copy(screen, process) }()
	defer func() {
		_ = screen.Close()
		_ = process.Close()
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
		deadline := time.Now().Add(8 * time.Second)
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
	// markPane writes a unique marker into the focused shell so each pane can
	// be located in the rendered frame independently of the others. The marker
	// is assembled by printf so the echoed command line does not contain it and
	// each pane matches exactly one rendered row.
	markPane := func(suffix string) {
		t.Helper()
		send("printf 'pane-%s\\n' " + suffix + "\r")
		wait("pane-" + suffix)
	}
	// paneRows reports which rendered rows contain a marker, which is how the
	// test distinguishes a side-by-side split from a stacked one.
	paneRows := func(marker string) []int {
		var rows []int
		for i, line := range strings.Split(screen.Render(), "\n") {
			if strings.Contains(line, marker) {
				rows = append(rows, i)
			}
		}
		return rows
	}
	wait("Open a session")
	send("n")
	wait("1 pane")
	markPane("one")

	send("\x02s") // stacked split: a new shell below
	wait("2 panes")
	markPane("two")
	send("\x02v") // side-by-side split of the second pane only
	wait("3 panes")
	markPane("three")

	first, second, third := paneRows("pane-one"), paneRows("pane-two"), paneRows("pane-three")
	if len(first) != 1 || len(second) != 1 || len(third) != 1 {
		t.Fatalf("expected three distinct panes, got rows %v %v %v:\n%s", first, second, third, screen.Render())
	}
	if second[0] != third[0] {
		t.Fatalf("v did not place the third pane beside the second: rows %v %v", second, third)
	}
	if first[0] >= second[0] {
		t.Fatalf("s did not place the second pane below the first: rows %v %v", first, second)
	}
	// Cycling focus does not disturb any pane's content.
	for i := 0; i < 3; i++ {
		send("\x02o")
	}
	wait("3 panes")
	for _, marker := range []string{"pane-one", "pane-two", "pane-three"} {
		if len(paneRows(marker)) != 1 {
			t.Fatalf("cycling focus disturbed %s:\n%s", marker, screen.Render())
		}
	}
	// Three cycles over three panes return focus to the third pane, so this
	// closes that pane. Its split collapses and the other shells stay alive.
	send("\x02q")
	wait("2 panes")
	for _, marker := range []string{"pane-one", "pane-two"} {
		if len(paneRows(marker)) != 1 {
			t.Fatalf("closing a pane disturbed %s:\n%s", marker, screen.Render())
		}
	}
	if rows := paneRows("pane-three"); len(rows) != 0 {
		t.Fatalf("closed pane is still rendered at %v:\n%s", rows, screen.Render())
	}
	send("\x02\t")
	wait("esc terminal")
	send("q")
	select {
	case <-process.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("split TUI did not quit")
	}
}

// Create a task with real keystrokes and see it appear in the sidebar. The
// panel was previously unreachable from the UI, so this covers the path a user
// actually takes rather than only Model.Update.
func TestProgramCreatesATaskWithRealKeys(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{{"init"}, {"config", "user.name", "F"}, {"config", "user.email", "f@example.invalid"}} {
		if b, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, b)
		}
	}
	_, socket := startEmbeddedTestDaemonWithSocket(t)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	process, err := pty.Start(pty.StartOptions{
		Command: executable, Arguments: []string{"-test.run=^TestTUIProcess$"},
		Columns: 150, Rows: 40,
		Env: append(os.Environ(), "TERM=xterm-256color", "ORKESTAR_TEST_TUI=1",
			"ORKESTAR_TEST_SOCKET="+socket, "ORKESTAR_TEST_DIRECTORY="+dir),
	})
	if err != nil {
		t.Fatal(err)
	}
	screen := terminal.NewScreen(150, 40)
	inputDone, outputDone := make(chan struct{}), make(chan struct{})
	go func() { defer close(inputDone); _, _ = io.Copy(process, screen) }()
	go func() { defer close(outputDone); _, _ = io.Copy(screen, process) }()
	defer func() {
		_ = screen.Close()
		_ = process.Close()
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
		deadline := time.Now().Add(8 * time.Second)
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
	wait("No tasks yet.")
	wait("Press c to create one.")
	send("\t")
	send("c")
	wait("New task")
	wait("Auto-review: on")
	// Tab inside the prompt toggles the reviewer gate rather than changing
	// the sidebar section.
	send("\t")
	wait("Auto-review: off")
	send("Ship it\r")
	wait("Task created")
	wait("Ship it")
	wait("pending")
	// The detail line explains what still has to happen for this task.
	wait("no worktree")
	send("q")
	select {
	case <-process.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("TUI did not quit")
	}
}
