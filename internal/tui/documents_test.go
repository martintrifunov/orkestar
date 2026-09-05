package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/martintrifunov/orkestar/internal/files"
)

func TestEditorSelectionUndoSaveAndConflict(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("hello\nworld\n"), 0644)
	d, err := files.Open(root, "a.txt")
	if err != nil {
		t.Fatal(err)
	}
	e := newTextEditor(d)
	e.key(tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
	e.Paste("λ revised\n")
	e.key(tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
	if string(e.text) != d.Text {
		t.Fatal("undo failed")
	}
	e.key(tea.KeyPressMsg{Code: 'y', Mod: tea.ModCtrl})
	e.key(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	b, _ := os.ReadFile(d.Path)
	if string(b) != "λ revised\n" || e.dirty() {
		t.Fatal("save failed")
	}
	e.click(6, 1, false)
	e.click(7, 1, true)
	if e.selected() != "λ" {
		t.Fatalf("mouse selection: %q", e.selected())
	}
	e.Paste("x")
	os.WriteFile(d.Path, []byte("agent wins\n"), 0644)
	e.key(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
	if !strings.Contains(e.status, "changed on disk") {
		t.Fatal("conflict was not shown")
	}
	m := Model{width: 120, height: 40}
	p := m.localPane("a", root, e)
	p.editor = e
	if m.canClose(p) {
		t.Fatal("dirty editor can be discarded silently")
	}
}
func TestReviewListsTrackedUntrackedAndSpaces(t *testing.T) {
	root := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git: %v %s", err, b)
		}
	}
	git("init")
	git("config", "user.name", "Fixture")
	git("config", "user.email", "fixture@example.invalid")
	os.WriteFile(filepath.Join(root, "code.go"), []byte("old\n"), 0644)
	git("add", ".")
	git("commit", "-m", "Initial")
	os.WriteFile(filepath.Join(root, "code.go"), []byte("new\n"), 0644)
	os.WriteFile(filepath.Join(root, "new file.txt"), []byte("untracked\n"), 0644)
	r := loadReview(root, 0)
	if r.err != nil || len(r.files) != 2 {
		t.Fatalf("review: %+v", r)
	}
	if !strings.Contains(r.diff, "-old") || !strings.Contains(r.diff, "+new") {
		t.Fatal("missing tracked diff")
	}
	r.selected = 1
	r.loadFile()
	if !strings.Contains(r.diff, "+untracked") || r.files[1].path != "new file.txt" {
		t.Fatal("untracked path/diff incorrect")
	}
}
func TestSplitKeysCreateAndCyclePanes(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	client := startEmbeddedTestDaemon(t)
	m := New(client, t.TempDir())
	m.width = 140
	m.height = 40
	first := startEmbeddedTestTerminal(t, client, []string{"/bin/sh", "-c", "printf ready; cat"})
	ready := openEmbeddedTerminal(client, first.ID, 80, 24)().(embeddedReadyMsg)
	if ready.err != nil {
		t.Fatal(ready.err)
	}
	m.addPane(ready.terminal)
	defer func() { m.closePanes() }()
	key := func(k tea.KeyPressMsg) tea.Cmd { updated, cmd := m.Update(k); m = updated.(Model); return cmd }
	key(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	cmd := key(tea.KeyPressMsg{Code: 'v'})
	if cmd == nil {
		t.Fatal("split with one pane did not open another")
	}
	updated, open := m.Update(cmd())
	m = updated.(Model)
	updated, _ = m.Update(open())
	m = updated.(Model)
	if len(m.visiblePanes()) != 2 || len(m.paneRects()) != 2 {
		t.Fatal("second pane not visible")
	}
	focused := m.embedded
	key(tea.KeyPressMsg{Code: tea.KeyF6})
	if m.embedded == focused {
		t.Fatal("F6 did not cycle")
	}
	key(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	key(tea.KeyPressMsg{Code: 's'})
	rects := m.paneRects()
	if rects[0].y == rects[1].y {
		t.Fatal("s did not stack panes")
	}
	key(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	key(tea.KeyPressMsg{Code: 'v'})
	rects = m.paneRects()
	if rects[0].x == rects[1].x {
		t.Fatal("v did not put panes side by side")
	}
	m.sidebarFocused = true
	focused = m.embedded
	key(tea.KeyPressMsg{Code: 'o'})
	if m.embedded == focused || m.sidebarFocused {
		t.Fatal("sidebar next did not focus next pane")
	}
}

func TestSearchPasteDoesNotEditDocument(t *testing.T) {
	root := t.TempDir()
	d, err := files.Open(root, "new.txt")
	if err != nil {
		t.Fatal(err)
	}
	e := newTextEditor(d)
	e.Paste("first λ match and second λ match")
	original := string(e.text)
	e.key(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	e.Paste("λ match")
	if string(e.text) != original || e.query != "λ match" {
		t.Fatal("search paste modified document")
	}
	e.key(tea.KeyPressMsg{Code: tea.KeyEnter})
	if e.selected() != "λ match" {
		t.Fatal("Unicode search failed")
	}
}

func TestEditorModeConfigurationRoundTrip(t *testing.T) {
	t.Setenv("ORKESTAR_TUI_CONFIG", filepath.Join(t.TempDir(), "config", "tui.json"))
	settings := editorSettings{Editor: "custom", Command: []string{"/path with spaces/editor", "--flag"}}
	if err := settings.save(); err != nil {
		t.Fatal(err)
	}
	loaded := readSettings()
	if loaded.Editor != settings.Editor || strings.Join(loaded.Command, "\x00") != strings.Join(settings.Command, "\x00") {
		t.Fatal("editor settings were not preserved")
	}
}
