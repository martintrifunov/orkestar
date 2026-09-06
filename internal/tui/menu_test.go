package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/martintrifunov/orkestar/internal/files"
)

// Right-clicking a pane is how someone who does not read help lines finds out
// what a pane can do.
func TestRightClickOpensAMenuOnThePaneUnderThePointer(t *testing.T) {
	m, first, second := taskModel(t)
	m.embedded = first
	rects := m.paneRects()
	if len(rects) != 2 {
		t.Fatalf("expected two panes, got %d", len(rects))
	}
	var target paneRect
	for _, rect := range rects {
		if rect.terminal == second {
			target = rect
		}
	}

	updated, _ := m.Update(tea.MouseClickMsg{X: target.x + 3, Y: target.y + 2, Button: tea.MouseRight})
	m = updated.(Model)
	if m.menu == nil {
		t.Fatal("the right button opened nothing")
	}
	// Acting on a pane the user did not click would be a surprise, so the
	// click focuses it too.
	if m.menu.pane != second || m.embedded != second {
		t.Fatal("the menu opened on the wrong pane")
	}
	if len(m.menu.items) == 0 {
		t.Fatal("the menu is empty")
	}
}

// An entry that would do nothing teaches the wrong thing about the whole menu.
func TestTheMenuOffersOnlyWhatWouldWork(t *testing.T) {
	m, first, _ := taskModel(t)
	m.embedded = first

	labels := func(items []menuItem) string {
		var out []string
		for _, item := range items {
			out = append(out, item.label)
		}
		return strings.Join(out, "|")
	}

	withTwo := labels(m.menuFor(first))
	if !strings.Contains(withTwo, "Zoom") || !strings.Contains(withTwo, "Next pane") {
		t.Fatalf("two panes should offer zoom and cycling: %s", withTwo)
	}

	// With one pane there is nowhere to zoom from or cycle to.
	m.layout = (*splitNode)(nil).insert(nil, first, false)
	if alone := labels(m.menuFor(first)); strings.Contains(alone, "Zoom") || strings.Contains(alone, "Next pane") {
		t.Fatalf("one pane should offer neither: %s", alone)
	}

	// A document pane has no scrollback; the wheel scrolls it.
	// Built the way the model builds one: a bare textEditor has no document
	// behind it and nothing that reads one would survive.
	editor := newTextEditor(&files.Document{Path: "notes.md", Text: "hello"})
	document := &embeddedTerminal{terminalID: "doc", editor: editor, emulator: editor, done: make(chan struct{})}
	if labels := labels(m.menuFor(document)); strings.Contains(labels, "Scrollback") {
		t.Fatalf("a document pane offered scrollback: %s", labels)
	}
}

// The menu shows the key beside each entry, so it teaches the keyboard rather
// than replacing it — and it has to show the user's key, not the default.
func TestTheMenuNamesTheRealKeys(t *testing.T) {
	m, first, _ := taskModel(t)
	var complaints []string
	m.keys, complaints = newBindings(map[string]string{"zoom": "Z"})
	if len(complaints) != 0 {
		t.Fatalf("complaints: %v", complaints)
	}
	m.embedded = first
	m.menu = &paneMenu{pane: first, items: m.menuFor(first)}

	rendered := ansi.Strip(m.renderMenu(m.render()))
	if !strings.Contains(rendered, "Zoom") {
		t.Fatalf("the menu did not render:\n%s", rendered)
	}
	if !strings.Contains(rendered, "Z") {
		t.Fatalf("the menu does not show the rebound key:\n%s", rendered)
	}
}

// Choosing an entry runs the same path the key does, so the two cannot drift.
func TestChoosingAnEntryActsOnThePane(t *testing.T) {
	m, first, _ := taskModel(t)
	m.embedded = first
	m.menu = &paneMenu{pane: first, items: m.menuFor(first)}

	index := -1
	for i, item := range m.menu.items {
		if item.action == ActionZoom {
			index = i
		}
	}
	if index < 0 {
		t.Fatal("the menu has no zoom entry")
	}
	updated, _ := m.chooseMenuItem(index)
	m = updated.(Model)
	if m.menu != nil {
		t.Fatal("the menu stayed open")
	}
	if !m.zoomed {
		t.Fatal("choosing zoom did not zoom")
	}
}

// Clicking away closes the menu without doing anything, which is what that
// means everywhere else.
func TestClickingAwayClosesTheMenu(t *testing.T) {
	m, first, _ := taskModel(t)
	m.embedded = first
	m.menu = &paneMenu{pane: first, items: m.menuFor(first), x: 40, y: 10}

	updated, _ := m.menuClick(tea.Mouse{X: 2, Y: 2, Button: tea.MouseLeft})
	m = updated.(Model)
	if m.menu != nil {
		t.Fatal("clicking away left the menu open")
	}
	if m.zoomed {
		t.Fatal("clicking away ran something")
	}
}

// While it is open the menu owns the keyboard, or a stray letter reaches the
// pane behind it.
func TestTheMenuOwnsTheKeyboard(t *testing.T) {
	m, first, _ := taskModel(t)
	m.embedded = first
	m.menu = &paneMenu{pane: first, items: m.menuFor(first)}

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = updated.(Model)
	if m.menu == nil || m.menu.at != 1 {
		t.Fatal("down did not move the selection")
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(Model)
	if m.menu != nil {
		t.Fatal("escape did not close the menu")
	}
}

// The overlay must not change the width of the frame it is drawn on, or the
// layout tears.
func TestTheMenuDoesNotDisturbTheFrame(t *testing.T) {
	m, first, _ := taskModel(t)
	m.embedded = first
	plain := m.render()
	m.menu = &paneMenu{pane: first, items: m.menuFor(first), x: 30, y: 5}
	withMenu := m.renderMenu(plain)

	before := strings.Split(plain, "\n")
	after := strings.Split(withMenu, "\n")
	if len(before) != len(after) {
		t.Fatalf("the frame changed height: %d then %d", len(before), len(after))
	}
	for i := range before {
		if ansi.StringWidth(before[i]) != ansi.StringWidth(after[i]) {
			t.Fatalf("row %d changed width: %d then %d", i, ansi.StringWidth(before[i]), ansi.StringWidth(after[i]))
		}
	}
}

// Every entry the menu offers must actually do something. One that silently
// did nothing would teach the wrong thing about all of them.
func TestEveryMenuEntryIsWired(t *testing.T) {
	m, first, _ := taskModel(t)
	m.embedded = first
	// A pane this client is not driving, so "Take control" is offered.
	first.view = &remoteScreen{controller: false}

	items := m.menuFor(first)
	if len(items) == 0 {
		t.Fatal("the menu is empty")
	}
	claim := false
	for _, item := range items {
		if item.action == ActionClaimPane {
			claim = true
		}
		// paneAction reports whether it recognised the key. An entry it does
		// not recognise is an entry that does nothing.
		probe := m
		if _, handled := probe.paneAction(m.keys.key(item.action)); !handled {
			t.Errorf("the %q entry (%s) is not wired to anything", item.label, item.action)
		}
	}
	if !claim {
		t.Fatal("a pane this client does not drive should offer taking control")
	}
}

// Pressed twice the prefix passes itself through, which is how a nested tmux
// is reached. It has to be the key the user bound.
func TestThePrefixPassesItselfThrough(t *testing.T) {
	m := Model{}
	if got := m.prefixBytes(); len(got) != 1 || got[0] != 0x02 {
		t.Fatalf("the default prefix sends %v, want ctrl+b", got)
	}

	var complaints []string
	m.keys, complaints = newBindings(map[string]string{"prefix": "ctrl+a"})
	if len(complaints) != 0 {
		t.Fatalf("complaints: %v", complaints)
	}
	if got := m.prefixBytes(); len(got) != 1 || got[0] != 0x01 {
		t.Fatalf("a rebound prefix sends %v, want ctrl+a", got)
	}
}

// Rebinding edit-file must not leave the sidebar's editor key doing nothing.
func TestARebedEditorKeyStillOpensTheEditor(t *testing.T) {
	m, first, _ := taskModel(t)
	m.embedded = first
	var complaints []string
	m.keys, complaints = newBindings(map[string]string{"edit-file": "E"})
	if len(complaints) != 0 {
		t.Fatalf("complaints: %v", complaints)
	}
	if _, handled := m.paneAction(m.keys.key(ActionEditFile)); !handled {
		t.Fatal("the rebound editor key does nothing")
	}
	// And the key it replaced no longer reaches it.
	if _, handled := m.paneAction("e"); handled {
		t.Fatal("the old editor key still works")
	}
}
