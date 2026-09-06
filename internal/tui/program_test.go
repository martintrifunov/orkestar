package tui

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

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
dd bs=1 count=7 2>/dev/null | od -An -tx1 | tr -d ' \n'
printf '\r\n'
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
	// Drive the outer terminal decoder and both IPC/PTY boundaries. Modified
	// Enter must reach every adapter intact, without becoming a submit byte.
	_, _ = process.Write([]byte("\x1b[13;2u"))
	wait(screen, "1b5b31333b3275")
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
	// panePositions reports the row and visual column of each marker. Column
	// distinguishes a side-by-side split from a stacked one; row alone cannot,
	// because a marker's row depends on how many lines its shell has printed,
	// which differs between panes under load.
	panePositions := func(marker string) [][2]int {
		var positions [][2]int
		for i, line := range strings.Split(screen.Render(), "\n") {
			if column := strings.Index(ansi.Strip(line), marker); column >= 0 {
				positions = append(positions, [2]int{i, column})
			}
		}
		return positions
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

	first, second, third := panePositions("pane-one"), panePositions("pane-two"), panePositions("pane-three")
	if len(first) != 1 || len(second) != 1 || len(third) != 1 {
		t.Fatalf("expected three distinct panes, got %v %v %v:\n%s", first, second, third, screen.Render())
	}
	if third[0][1] <= second[0][1] {
		t.Fatalf("v did not place the third pane right of the second: %v %v", second, third)
	}
	if first[0][0] >= second[0][0] || first[0][0] >= third[0][0] {
		t.Fatalf("s did not place the later panes below the first: %v %v %v", first, second, third)
	}
	// Cycling focus does not disturb any pane's content.
	for i := 0; i < 3; i++ {
		send("\x02o")
	}
	wait("3 panes")
	for _, marker := range []string{"pane-one", "pane-two", "pane-three"} {
		if len(panePositions(marker)) != 1 {
			t.Fatalf("cycling focus disturbed %s:\n%s", marker, screen.Render())
		}
	}
	// Three cycles over three panes return focus to the third pane, so this
	// closes that pane. Its split collapses and the other shells stay alive.
	send("\x02q")
	wait("2 panes")
	for _, marker := range []string{"pane-one", "pane-two"} {
		if len(panePositions(marker)) != 1 {
			t.Fatalf("closing a pane disturbed %s:\n%s", marker, screen.Render())
		}
	}
	if positions := panePositions("pane-three"); len(positions) != 0 {
		t.Fatalf("closed pane is still rendered at %v:\n%s", positions, screen.Render())
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
	// Ctrl+R inside the prompt toggles the reviewer gate. Tab moves between
	// the title and description rather than changing the sidebar section.
	send("\x12")
	wait("Auto-review: off")
	send("Ship it")
	send("\t")
	send("before the release")
	wait("before the release")
	send("\r")
	wait("Task created")
	wait("Ship it")
	wait("pending")
	// The selected task shows why it exists, then what still has to happen.
	wait("before the release")
	wait("no worktree")

	// The same prompt reopens over the task to correct it.
	send("e")
	wait("Edit task")
	send("\x7f\x7f")
	wait("Ship")
	send("\r")
	wait("Task updated")
	send("q")
	select {
	case <-process.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("TUI did not quit")
	}
}

// Open the file viewer with its shortcut in a real terminal and watch it pick
// up a file an agent creates while it is open.
func TestProgramFileViewerOpensAndFollowsTheWorkspace(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{{"init"}, {"config", "user.name", "F"}, {"config", "user.email", "f@example.invalid"}} {
		if b, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, b)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "internal"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"main.go", "internal/first.go"} {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), []byte("package main\n"), 0o644); err != nil {
			t.Fatal(err)
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
		deadline := time.Now().Add(10 * time.Second)
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
	if strings.Contains(screen.Render(), "Files") {
		t.Fatalf("the viewer is not collapsed by default:\n%s", screen.Render())
	}
	send("\x02f")
	wait("Files")
	wait("main.go")
	wait("internal")
	// The tree is collapsed, so a nested file only shows once expanded.
	if strings.Contains(screen.Render(), "first.go") {
		t.Fatal("directories are not collapsed on open")
	}
	// A file appearing on disk shows up without any keystroke.
	if err := os.WriteFile(filepath.Join(dir, "written-by-agent.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wait("written-by-agent.go")
	send("\x02f")
	deadline := time.Now().Add(3 * time.Second)
	for strings.Contains(screen.Render(), "written-by-agent.go") {
		if time.Now().After(deadline) {
			t.Fatalf("the viewer did not close:\n%s", screen.Render())
		}
		time.Sleep(10 * time.Millisecond)
	}
	send("q")
	select {
	case <-process.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("TUI did not quit")
	}
}

// Rebinding has to work in the real interface, not only in the map: the
// dispatch, the prefix and the help line all read from it, and a unit test
// proves none of that reached the keyboard.
func TestProgramHonoursRebedKeys(t *testing.T) {
	dir := t.TempDir()
	_, socket := startEmbeddedTestDaemonWithSocket(t)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	// A tmux user's prefix, and a new-task key their fingers already know.
	config := filepath.Join(t.TempDir(), "tui.json")
	settings := `{"editor":"standard","keys":{"prefix":"ctrl+a","new-task":"N"}}`
	if err := os.WriteFile(config, []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}

	process, err := pty.Start(pty.StartOptions{
		Command: executable, Arguments: []string{"-test.run=^TestTUIProcess$"},
		Columns: 150, Rows: 40,
		Env: append(os.Environ(), "TERM=xterm-256color", "ORKESTAR_TEST_TUI=1",
			"ORKESTAR_TEST_SOCKET="+socket, "ORKESTAR_TEST_DIRECTORY="+dir,
			"ORKESTAR_TUI_CONFIG="+config),
	})
	if err != nil {
		t.Fatal(err)
	}
	screen := terminal.NewScreen(150, 40)
	inputDone, outputDone := make(chan struct{}), make(chan struct{})
	go func() { defer close(inputDone); _, _ = io.Copy(process, screen) }()
	go func() { defer close(outputDone); _, _ = io.Copy(screen, process) }()
	// The screen has to be closed too, or the copy out of it never returns
	// and the cleanup blocks until the whole package times out.
	t.Cleanup(func() {
		_ = screen.Close()
		_ = process.Close()
		for _, done := range []chan struct{}{inputDone, outputDone} {
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Error("TUI worker did not stop")
			}
		}
	})

	wait := func(want string) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
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
	// The help line names the rebound key rather than the default.
	send("\t")
	wait("N new")

	// And the key itself does the thing.
	send("N")
	wait("New task")

	// Escape closes it, and the screen has to actually repaint before the next
	// assertion means anything.
	send("\x1b")
	gone := func(what string) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			if !strings.Contains(screen.Render(), what) {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("%q never went away:\n%s", what, screen.Render())
	}
	gone("New task")

	// The old key is now just a keystroke, not a second way in.
	send("c")
	time.Sleep(300 * time.Millisecond)
	if strings.Contains(screen.Render(), "New task") {
		t.Fatalf("the default key still opens the prompt:\n%s", screen.Render())
	}

	// The rebound prefix arms the pane actions; the old one does not.
	send("\x01")
	wait("Prefix:")
	send("\x1b")
	gone("Prefix:")
	send("q")
	select {
	case <-process.Done():
	case <-time.After(5 * time.Second):
		t.Fatalf("TUI did not quit:\n%s", screen.Render())
	}
}
