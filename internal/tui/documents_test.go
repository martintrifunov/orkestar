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
	m.width = 160
	m.height = 44
	first := startEmbeddedTestTerminal(t, client, []string{"/bin/sh", "-c", "printf ready; cat"})
	ready := openEmbeddedTerminal(client, first.ID, 80, 24)().(embeddedReadyMsg)
	if ready.err != nil {
		t.Fatal(ready.err)
	}
	m.addPane(ready.terminal)
	defer func() { m.closePanes() }()
	key := func(k tea.KeyPressMsg) tea.Cmd { updated, cmd := m.Update(k); m = updated.(Model); return cmd }
	rectOf := func(p *embeddedTerminal) paneRect {
		for _, r := range m.paneRects() {
			if r.terminal == p {
				return r
			}
		}
		t.Fatalf("pane %s has no rect", p.terminalID)
		return paneRect{}
	}
	// split runs the whole launch: Ctrl+b, the key, the daemon start reply and
	// the attach reply. Every split must yield a new pane.
	split := func(k rune) *embeddedTerminal {
		t.Helper()
		before := len(m.visiblePanes())
		key(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
		cmd := key(tea.KeyPressMsg{Code: k})
		if cmd == nil {
			t.Fatalf("%c did not start a shell", k)
		}
		updated, open := m.Update(cmd())
		m = updated.(Model)
		updated, _ = m.Update(open())
		m = updated.(Model)
		if len(m.visiblePanes()) != before+1 || len(m.paneRects()) != before+1 {
			t.Fatalf("%c: %d panes, want %d", k, len(m.visiblePanes()), before+1)
		}
		return m.embedded
	}
	one := m.embedded
	two := split('v')
	if a, b := rectOf(one), rectOf(two); a.y != b.y || a.x >= b.x {
		t.Fatalf("v did not put the new pane beside the focused one: %+v %+v", a, b)
	}
	untouched := rectOf(one)
	three := split('s')
	if a, b := rectOf(two), rectOf(three); a.x != b.x || a.y >= b.y {
		t.Fatalf("s did not put the new pane below the focused one: %+v %+v", a, b)
	}
	if rectOf(one) != untouched {
		t.Fatalf("stacking the second pane moved the first: %+v -> %+v", untouched, rectOf(one))
	}
	four := split('v')
	if a, b := rectOf(three), rectOf(four); a.y != b.y || a.x >= b.x || rectOf(one) != untouched {
		t.Fatal("mixed split did not create a fourth pane beside the third")
	}
	if len(m.visiblePanes()) != 4 {
		t.Fatal("expected four distinct panes")
	}
	focused := m.embedded
	key(tea.KeyPressMsg{Code: tea.KeyF6})
	if m.embedded == focused {
		t.Fatal("F6 did not cycle")
	}
	m.sidebarFocused = true
	focused = m.embedded
	key(tea.KeyPressMsg{Code: 'o'})
	if m.embedded == focused || m.sidebarFocused {
		t.Fatal("sidebar next did not focus next pane")
	}
	// Closing a middle pane collapses its split; the sibling inherits the space.
	m.embedded = two
	key(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	key(tea.KeyPressMsg{Code: 'q'})
	if len(m.visiblePanes()) != 3 || m.embedded != three {
		t.Fatalf("close did not collapse onto the sibling: %d panes, focus %v", len(m.visiblePanes()), m.embedded)
	}
	if r := rectOf(three); r.y != untouched.y || r.height+rectOf(four).height < untouched.height {
		t.Fatalf("sibling did not take over the closed pane's space: %+v", r)
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
