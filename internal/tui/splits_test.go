package tui

import (
	"errors"
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/martintrifunov/orkestar/internal/daemon"
)

func TestSplitTreeInsertRemoveAndOrder(t *testing.T) {
	a, b, c, d := &embeddedTerminal{terminalID: "a"}, &embeddedTerminal{terminalID: "b"}, &embeddedTerminal{terminalID: "c"}, &embeddedTerminal{terminalID: "d"}
	var root *splitNode
	root = root.insert(nil, a, false)
	root = root.insert(a, b, false)
	root = root.insert(b, c, true)
	root = root.insert(a, d, true)
	order := func() string {
		s := ""
		for _, p := range root.leaves(nil) {
			s += p.terminalID
		}
		return s
	}
	if order() != "adbc" {
		t.Fatalf("leaf order %q", order())
	}
	rects := root.rects(0, 0, 100, 40, nil)
	byID := map[string]paneRect{}
	for _, r := range rects {
		byID[r.terminal.terminalID] = r
	}
	if byID["a"] != (paneRect{a, 0, 0, 50, 20}) || byID["d"] != (paneRect{d, 0, 20, 50, 20}) || byID["b"] != (paneRect{b, 50, 0, 50, 20}) || byID["c"] != (paneRect{c, 50, 20, 50, 20}) {
		t.Fatalf("unexpected geometry: %+v", byID)
	}
	if got := root.sibling(b); len(got) != 1 || got[0] != c {
		t.Fatal("sibling lookup failed")
	}
	root = root.remove(b)
	if order() != "adc" || root.rects(0, 0, 100, 40, nil)[2] != (paneRect{c, 50, 0, 50, 40}) {
		t.Fatalf("removing b did not hand its space to c: %q %+v", order(), root.rects(0, 0, 100, 40, nil))
	}
	// An unknown target splits the whole layout instead of being dropped.
	root = root.insert(b, &embeddedTerminal{terminalID: "e"}, true)
	if order() != "adce" || root.rects(0, 0, 100, 40, nil)[3].y != 20 {
		t.Fatalf("root split failed: %q", order())
	}
	for _, p := range root.leaves(nil) {
		root = root.remove(p)
	}
	if root != nil {
		t.Fatal("removing every pane must empty the tree")
	}
}

func TestManyPanesTileResizeAndCollapse(t *testing.T) {
	m := Model{width: 260, height: 90}
	m.addPane(fakePane(t, "0"))
	for i := 1; i < 9; i++ {
		// Split whichever pane is largest, like a user spreading work out.
		var largest paneRect
		for _, r := range m.paneRects() {
			if r.width*r.height > largest.width*largest.height {
				largest = r
			}
		}
		m.insertPane(fakePane(t, fmt.Sprint(i)), largest.terminal, m.autoStacked(largest.terminal))
	}
	assertTiled(t, m, 9)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 300, Height: 100})
	m = updated.(Model)
	assertTiled(t, m, 9)
	if m.width != 300 {
		t.Fatal("resize was not applied")
	}
	victim := m.visiblePanes()[4]
	m.embedded = victim
	m.removePane(victim)
	assertTiled(t, m, 8)
	if m.embedded == victim || m.embedded == nil {
		t.Fatal("focus stayed on a closed pane")
	}
	for _, p := range m.visiblePanes() {
		if p == victim {
			t.Fatal("closed pane still in layout")
		}
	}
	for i := 0; i < 8; i++ {
		m.nextPane()
	}
	if m.embedded != m.visiblePanes()[0] && len(m.visiblePanes()) != 8 {
		t.Fatal("cycling lost panes")
	}
}

func TestSplitTargetSurvivesFocusChangeAndFailure(t *testing.T) {
	t.Setenv("SHELL", "/bin/sh")
	m := New(nil, t.TempDir())
	m.width, m.height = 160, 44
	a, b := fakePane(t, "a"), fakePane(t, "b")
	m.addPane(a)
	m.insertPane(b, a, false)
	m.embedded = a
	if _, ok := m.paneAction("s"); !ok || m.pendingSplit == nil || m.pendingSplit.target != a || !m.pendingSplit.stacked || !m.opening {
		t.Fatal("split request was not recorded")
	}
	m.embedded = b // focus moves while the shell starts
	updated, _ := m.Update(embeddedReadyMsg{terminal: fakePane(t, "c")})
	m = updated.(Model)
	rects := map[string]paneRect{}
	for _, r := range m.paneRects() {
		rects[r.terminal.terminalID] = r
	}
	if rects["c"].x != rects["a"].x || rects["c"].y <= rects["a"].y || rects["b"].height != 41 {
		t.Fatalf("new pane did not land below its original target: %+v", rects)
	}
	if m.pendingSplit != nil || m.opening {
		t.Fatal("split request was not consumed")
	}
	// A second split cannot start while one is in flight.
	m.paneAction("v")
	if _, ok := m.paneAction("s"); !ok || m.pendingSplit.stacked {
		t.Fatal("a pending split was overwritten")
	}
	// Launch failure clears the request instead of misplacing the next pane.
	updated, _ = m.Update(terminalStartedMsg{err: errors.New("no shell")})
	m = updated.(Model)
	if m.pendingSplit != nil || m.opening {
		t.Fatal("failed launch left a stale split request")
	}
	// A target closed during the launch falls back to the focused pane.
	m.paneAction("v")
	m.embedded = a
	m.removePane(a)
	updated, _ = m.Update(embeddedReadyMsg{terminal: fakePane(t, "d")})
	m = updated.(Model)
	if len(m.visiblePanes()) != 3 || m.embedded.terminalID != "d" || m.pendingSplit != nil {
		t.Fatalf("closed target should not lose the new pane: %d panes", len(m.visiblePanes()))
	}
}

func TestPaneLimitIsConfigurableAndNeverReplaces(t *testing.T) {
	m := New(nil, t.TempDir())
	m.width, m.height = 160, 44
	m.settings.MaxPanes = 2
	m.addPane(fakePane(t, "a"))
	m.addPane(fakePane(t, "b"))
	if cmd, _ := m.paneAction("v"); cmd != nil || m.opening || m.notice == "" {
		t.Fatal("split above the limit must be refused with a notice")
	}
	late := fakePane(t, "c")
	late.done = make(chan struct{})
	updated, _ := m.Update(embeddedReadyMsg{terminal: late})
	m = updated.(Model)
	if len(m.visiblePanes()) != 2 {
		t.Fatal("a late attachment replaced or exceeded the open panes")
	}
	select {
	case <-late.done:
	default:
		t.Fatal("refused attachment was not closed")
	}
	if (editorSettings{}).paneLimit() != defaultMaxPanes || (editorSettings{MaxPanes: 500}).paneLimit() != maxPaneLimit {
		t.Fatal("pane limit defaults or clamp are wrong")
	}
	m.snapshot.Terminals = []daemon.Terminal{{ID: "term_x"}}
	m.sidebarFocused = true
	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if cmd != nil || m.opening {
		t.Fatal("opening a session above the limit should not attach")
	}
}
