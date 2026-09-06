package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

const paneColumns = 20

// selectionOver returns the selection a drag from one cell to another makes.
func selectionOver(fromX, fromY, toX, toY int) paneSelection {
	var s paneSelection
	s.begin(fromX, fromY)
	s.extend(toX, toY)
	return s
}

func TestSelectionCopiesWhatItCovers(t *testing.T) {
	content := strings.Join([]string{"first line", "second line", "third line"}, "\n")
	for _, test := range []struct {
		name      string
		selection paneSelection
		want      string
	}{
		{"one cell", selectionOver(0, 0, 0, 0), "f"},
		{"part of a row", selectionOver(0, 0, 4, 0), "first"},
		{"whole row", selectionOver(0, 1, paneColumns-1, 1), "second line"},
		{"across rows", selectionOver(6, 0, 5, 1), "line\nsecond"},
		{"through a whole row", selectionOver(6, 0, 5, 2), "line\nsecond line\nthird"},
		{"dragged backwards", selectionOver(4, 0, 0, 0), "first"},
		{"dragged up", selectionOver(5, 1, 6, 0), "line\nsecond"},
		{"past the end of a row", selectionOver(0, 2, paneColumns-1, 2), "third line"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := test.selection.text(content, paneColumns); got != test.want {
				t.Fatalf("got %q, want %q", got, test.want)
			}
		})
	}
}

func TestNoSelectionCopiesNothing(t *testing.T) {
	if got := (paneSelection{}).text("first line", paneColumns); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

// Styling is what a terminal pane's rows are full of, and a selection has to
// count columns through it rather than counting bytes.
func TestSelectionCountsColumnsThroughStyling(t *testing.T) {
	content := "\x1b[31mred\x1b[m plain \x1b[1mbold\x1b[m"
	if got := selectionOver(4, 0, 8, 0).text(content, paneColumns); got != "plain" {
		t.Fatalf("got %q, want %q", got, "plain")
	}
}

// The highlight has to cover exactly the selected columns and leave the rest
// of the row alone, including the row's own colours outside it.
func TestHighlightCoversTheSelectedColumns(t *testing.T) {
	content := "\x1b[31mred\x1b[m plain"
	highlighted := selectionOver(4, 0, 8, 0).highlight(content, paneColumns)
	if plain := ansi.Strip(highlighted); plain != "red plain" {
		t.Fatalf("highlight changed the text to %q", plain)
	}
	if ansi.StringWidth(highlighted) != ansi.StringWidth(content) {
		t.Fatalf("highlight changed the row width from %d to %d", ansi.StringWidth(content), ansi.StringWidth(highlighted))
	}
	if !strings.Contains(highlighted, selectionStyle.Render("plain")) {
		t.Fatalf("selected columns are not highlighted: %q", highlighted)
	}
	if strings.Contains(highlighted, selectionStyle.Render("red")) {
		t.Fatalf("unselected columns are highlighted: %q", highlighted)
	}
}

// Selecting past the end of a row must still draw a highlight there, or a drag
// through blank space looks like it did nothing.
func TestHighlightPadsShortRows(t *testing.T) {
	highlighted := selectionOver(0, 0, 9, 0).highlight("ab", paneColumns)
	if width := ansi.StringWidth(highlighted); width != 10 {
		t.Fatalf("highlighted row is %d columns wide, want 10", width)
	}
}

func TestSelectionSurvivesAnEmptyPane(t *testing.T) {
	if got := selectionOver(0, 4, 3, 6).text("only one row", paneColumns); got != "" {
		t.Fatalf("got %q for a selection below the content, want empty", got)
	}
	if got := selectionOver(0, 4, 3, 6).highlight("only one row", paneColumns); got != "only one row" {
		t.Fatalf("highlight below the content changed the row to %q", got)
	}
}

// A pane whose program took the mouse over gets the events, and shift is how
// the user reaches selection anyway.
func TestSelectableRespectsMouseReporting(t *testing.T) {
	plain := &embeddedTerminal{terminalID: "plain"}
	reporting := &embeddedTerminal{terminalID: "mouse", view: &remoteScreen{controller: true}}
	reporting.view.frame.Mouse = true
	document := &embeddedTerminal{terminalID: "doc", editor: &textEditor{}}

	var m Model
	for _, test := range []struct {
		name  string
		pane  *embeddedTerminal
		shift bool
		want  bool
	}{
		{"plain terminal", plain, false, true},
		{"terminal reporting the mouse", reporting, false, false},
		{"terminal reporting the mouse, shift held", reporting, true, true},
		{"document pane", document, false, false},
		{"document pane, shift held", document, true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := m.selectable(test.pane, test.shift); got != test.want {
				t.Fatalf("selectable = %v, want %v", got, test.want)
			}
		})
	}
}

// The whole gesture: press, drag, release, and the covered text reaches the
// clipboard.
func TestDragOverAPaneCopiesToTheClipboard(t *testing.T) {
	pane := &embeddedTerminal{terminalID: "t", title: "shell", emulator: &staticScreen{content: "first line\nsecond line"}, done: make(chan struct{})}
	model := Model{width: 120, height: 40, layout: (*splitNode)(nil).insert(nil, pane, false), embedded: pane}

	rects := model.paneRects()
	if len(rects) != 1 {
		t.Fatalf("expected one pane, got %d", len(rects))
	}
	origin := rects[0]

	next, _ := model.Update(tea.MouseClickMsg{X: origin.x + 2, Y: origin.y + 1, Button: tea.MouseLeft})
	model = next.(Model)
	next, _ = model.Update(tea.MouseMotionMsg{X: origin.x + 2 + 4, Y: origin.y + 1, Button: tea.MouseLeft})
	model = next.(Model)
	if !pane.selection.dragging {
		t.Fatal("drag did not start")
	}

	next, cmd := model.Update(tea.MouseReleaseMsg{X: origin.x + 2 + 4, Y: origin.y + 1, Button: tea.MouseLeft})
	model = next.(Model)
	if pane.selection.dragging {
		t.Fatal("drag did not end on release")
	}
	if cmd == nil {
		t.Fatal("release produced no clipboard command")
	}
	// The clipboard message type is unexported, so compare against the
	// message the same command produces for the text we expect.
	if got, want := cmd(), tea.SetClipboard("first")(); got != want {
		t.Fatalf("copied %v, want %v", got, want)
	}
	if !strings.Contains(model.notice, "Copied 1 line") {
		t.Fatalf("notice is %q", model.notice)
	}
}

// Clicking to focus a pane must leave the clipboard alone.
func TestClickWithoutDraggingCopiesNothing(t *testing.T) {
	pane := &embeddedTerminal{terminalID: "t", emulator: &staticScreen{content: "first line"}, done: make(chan struct{})}
	model := Model{width: 120, height: 40, layout: (*splitNode)(nil).insert(nil, pane, false), embedded: pane}
	origin := model.paneRects()[0]

	next, _ := model.Update(tea.MouseClickMsg{X: origin.x + 2, Y: origin.y + 1, Button: tea.MouseLeft})
	model = next.(Model)
	next, cmd := model.Update(tea.MouseReleaseMsg{X: origin.x + 2, Y: origin.y + 1, Button: tea.MouseLeft})
	model = next.(Model)

	if cmd != nil {
		t.Fatalf("a click produced %v", cmd())
	}
	if pane.selection.present {
		t.Fatal("a click left a highlight behind")
	}
	if model.notice != "" {
		t.Fatalf("a click set the notice to %q", model.notice)
	}
}

// A highlight that outlived the cells under it would point at the wrong text,
// so typing into the pane drops it.
func TestTypingClearsTheSelection(t *testing.T) {
	pane := &embeddedTerminal{terminalID: "t", emulator: &staticScreen{content: "first line"}, done: make(chan struct{})}
	pane.selection = selectionOver(0, 0, 4, 0)
	model := Model{width: 120, height: 40, layout: (*splitNode)(nil).insert(nil, pane, false), embedded: pane}

	if _, _ = model.updateEmbedded(tea.KeyPressMsg{Code: 'x', Text: "x"}); pane.selection.present {
		t.Fatal("selection survived a keystroke")
	}
}
