package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func twoPanes(t *testing.T) Model {
	t.Helper()
	m := Model{width: 160, height: 44}
	m.addPane(fakePane(t, "0"))
	m.insertPane(fakePane(t, "1"), m.embedded, false)
	return m
}

func TestZoomGivesOnePaneTheWholeAreaWithoutLosingTheOthers(t *testing.T) {
	m := twoPanes(t)
	m.insertPane(fakePane(t, "2"), m.embedded, true)
	assertTiled(t, m, 3)
	focused := m.embedded

	if _, ok := m.paneAction("z"); !ok || !m.zoomed {
		t.Fatal("z did not zoom")
	}
	rects := m.paneRects()
	x, y, width, height := m.contentArea()
	if len(rects) != 1 || rects[0].terminal != focused {
		t.Fatalf("zoom did not isolate the focused pane: %+v", rects)
	}
	if rects[0].x != x || rects[0].y != y || rects[0].width != width || rects[0].height != height {
		t.Fatalf("zoomed pane does not fill the content area: %+v", rects[0])
	}
	if len(m.visiblePanes()) != 3 {
		t.Fatal("zoom dropped panes from the tree")
	}
	if out := ansi.Strip(m.render()); !strings.Contains(out, "zoomed") {
		t.Fatal("the header does not say the pane is zoomed")
	}
	// Cycling still reaches the hidden panes, and the newly focused one fills
	// the area instead.
	m.nextPane()
	if m.paneRects()[0].terminal == focused {
		t.Fatal("cycling while zoomed did not change the visible pane")
	}
	if _, ok := m.paneAction("z"); !ok || m.zoomed {
		t.Fatal("z did not unzoom")
	}
	assertTiled(t, m, 3)

	// A single pane has nothing to zoom out of.
	single := Model{width: 160, height: 44}
	single.addPane(fakePane(t, "only"))
	single.paneAction("z")
	if single.zoomed || !strings.Contains(single.notice, "Only one pane") {
		t.Fatalf("zoom with one pane: zoomed=%v notice=%q", single.zoomed, single.notice)
	}
}

func TestPanesAreLabelledInTheirBorder(t *testing.T) {
	m := Model{width: 160, height: 44}
	editor := editorFor(t, "main.go", "package main\n")
	pane := m.localPane("main.go", t.TempDir(), editor)
	pane.editor = editor
	m.insertPane(fakePane(t, "term_1"), m.embedded, false)

	out := ansi.Strip(m.renderPanes())
	if !strings.Contains(out, "╭─ main.go ") {
		t.Fatalf("the editor pane is not labelled:\n%s", out)
	}
	if !strings.Contains(out, "╭─ term_1 ") {
		t.Fatalf("the terminal pane is not labelled:\n%s", out)
	}
	// A label never widens or heightens the box.
	for _, line := range strings.Split(out, "\n") {
		if ansi.StringWidth(line) > m.width {
			t.Fatalf("a labelled row is %d cells wide: %q", ansi.StringWidth(line), line)
		}
	}
	if lipgloss.Height(m.renderPanes()) != m.height-3 {
		t.Fatalf("labels changed the pane height: %d", lipgloss.Height(m.renderPanes()))
	}
	// A name longer than the pane is truncated rather than breaking the border.
	long := Model{width: 90, height: 30}
	long.addPane(fakePane(t, strings.Repeat("verylongname", 12)))
	for _, line := range strings.Split(ansi.Strip(long.renderPanes()), "\n") {
		if ansi.StringWidth(line) > long.width {
			t.Fatalf("a long label overflowed: %q", line)
		}
	}
}

func TestSplitsResizeFromTheKeyboardAndStayTiled(t *testing.T) {
	m := twoPanes(t)
	before := m.paneRects()[0].width
	if _, ok := m.paneAction("right"); !ok {
		t.Fatal("arrow resize was not handled")
	}
	after := m.paneRects()[0].width
	if after <= before {
		t.Fatalf("right did not grow the first pane: %d -> %d", before, after)
	}
	assertTiled(t, m, 2)
	for i := 0; i < 200; i++ {
		m.paneAction("left")
	}
	assertTiled(t, m, 2)
	if m.paneRects()[0].width < minPaneWidth {
		t.Fatalf("a pane was squeezed below the minimum: %d", m.paneRects()[0].width)
	}
	for i := 0; i < 200; i++ {
		m.paneAction("right")
	}
	assertTiled(t, m, 2)
	if m.paneRects()[1].width < minPaneWidth {
		t.Fatalf("the sibling was squeezed below the minimum: %d", m.paneRects()[1].width)
	}
	// Resizing along an axis with no split says so rather than doing nothing.
	m.paneAction("up")
	if !strings.Contains(m.notice, "No split to resize") {
		t.Fatalf("a missing split was not reported: %q", m.notice)
	}
}

func TestDividersCanBeDragged(t *testing.T) {
	m := twoPanes(t)
	dividers := m.dividers()
	if len(dividers) != 1 || !dividers[0].vertical {
		t.Fatalf("expected one vertical divider: %+v", dividers)
	}
	line := dividers[0].at()
	before := m.paneRects()[0].width

	updated, _ := m.mouseClick(tea.MouseClickMsg{X: line, Y: dividers[0].y + 2, Button: tea.MouseLeft})
	m = updated.(Model)
	if m.dragging == nil {
		t.Fatal("clicking the divider did not start a drag")
	}
	if m.embedded != m.visiblePanes()[1] {
		t.Fatal("grabbing the divider changed the focused pane")
	}
	updated, _ = m.Update(tea.MouseMotionMsg{X: line + 20, Y: dividers[0].y + 2, Button: tea.MouseLeft})
	m = updated.(Model)
	if m.paneRects()[0].width <= before {
		t.Fatalf("dragging right did not widen the first pane: %d", m.paneRects()[0].width)
	}
	assertTiled(t, m, 2)
	updated, _ = m.Update(tea.MouseReleaseMsg{X: line + 20, Y: dividers[0].y + 2, Button: tea.MouseLeft})
	m = updated.(Model)
	if m.dragging != nil {
		t.Fatal("release did not end the drag")
	}
	// Dragging beyond the edge is clamped, not allowed to erase a pane.
	updated, _ = m.mouseClick(tea.MouseClickMsg{X: m.dividers()[0].at(), Y: dividers[0].y + 2, Button: tea.MouseLeft})
	m = updated.(Model)
	updated, _ = m.Update(tea.MouseMotionMsg{X: 0, Y: dividers[0].y + 2, Button: tea.MouseLeft})
	m = updated.(Model)
	assertTiled(t, m, 2)
	if m.paneRects()[0].width < minPaneWidth {
		t.Fatalf("a drag squeezed a pane out: %d", m.paneRects()[0].width)
	}
	// A zoomed layout has nothing to drag.
	m.dragging = nil
	m.zoomed = true
	if len(m.dividers()) != 0 {
		t.Fatal("a zoomed layout still offered dividers")
	}
}

// Resizing used to move a couple of columns per press and needed the prefix
// re-armed every time, which made dragging a divider from the keyboard tedious.
func TestResizeStepsAreUsableAndRepeatWithoutReArmingThePrefix(t *testing.T) {
	m := twoPanes(t)
	start := m.paneRects()[0].width

	// One press moves a visible amount, not a couple of columns.
	m.paneAction("right")
	moved := m.paneRects()[0].width - start
	if moved < 5 {
		t.Fatalf("a resize press moved only %d columns", moved)
	}

	// Arrows keep the prefix armed, so they can be pressed in a row.
	key := func(k tea.KeyPressMsg) Model {
		updated, _ := m.Update(k)
		return updated.(Model)
	}
	m = key(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	if !m.prefix {
		t.Fatal("ctrl+b did not arm the prefix")
	}
	before := m.paneRects()[0].width
	m = key(tea.KeyPressMsg{Code: tea.KeyRight})
	if !m.prefix {
		t.Fatal("the prefix was not kept armed for a repeat")
	}
	m = key(tea.KeyPressMsg{Code: tea.KeyRight})
	if m.paneRects()[0].width <= before+moved {
		t.Fatal("the second arrow did not resize without a new prefix")
	}
	// Esc ends the repeat and is not passed on to the pane.
	m = key(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.prefix {
		t.Fatal("esc did not end the repeat")
	}
	// With the prefix gone the arrow is an ordinary key again. Read it from
	// the sidebar, where a stray arrow cannot reach a pane's emulator.
	width := m.paneRects()[0].width
	m.sidebarFocused = true
	m = key(tea.KeyPressMsg{Code: tea.KeyRight})
	if m.paneRects()[0].width != width {
		t.Fatal("an arrow still resized after the repeat ended")
	}
	m.sidebarFocused = false
	// A non-repeating action disarms the prefix as before.
	m = key(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	m = key(tea.KeyPressMsg{Code: 'z'})
	if m.prefix || !m.zoomed {
		t.Fatalf("zoom should not repeat: prefix=%v zoomed=%v", m.prefix, m.zoomed)
	}
	assertTiled(t, m, 1)
}
