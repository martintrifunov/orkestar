package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

var selectionStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#111111")).Background(lipgloss.Color("#D7A84B"))

// paneSelection is a range of a terminal pane's visible screen, in the pane's
// own content coordinates: column 0, row 0 is the first cell inside the
// border. It selects the way a terminal does, flowing from the first cell to
// the last through the ends of the rows between them, rather than as a block.
//
// The coordinates address the screen, not the text, so a selection made while
// output is still arriving covers whatever ends up in those cells. That
// matches what the user is pointing at.
type paneSelection struct {
	present  bool
	dragging bool
	// anchor is where the drag started and cursor is where the pointer is.
	// Either may come first on screen.
	anchorX, anchorY int
	cursorX, cursorY int
}

// begin starts a drag at a cell, discarding any earlier selection.
func (s *paneSelection) begin(x, y int) {
	*s = paneSelection{present: true, dragging: true, anchorX: x, anchorY: y, cursorX: x, cursorY: y}
}

// extend moves the loose end of a drag. It is a no-op when no drag is running,
// so pointer movement over an unselected pane costs nothing.
func (s *paneSelection) extend(x, y int) bool {
	if !s.dragging {
		return false
	}
	s.cursorX, s.cursorY = x, y
	return true
}

// clear drops the selection, which every keystroke and pane change does: a
// highlight that outlives what it was pointing at is worse than none.
func (s *paneSelection) clear() { *s = paneSelection{} }

// bounds returns the selection in reading order, with the end column
// exclusive, and reports whether it covers any cell at all.
func (s paneSelection) bounds() (startX, startY, endX, endY int, ok bool) {
	if !s.present {
		return 0, 0, 0, 0, false
	}
	startX, startY, endX, endY = s.anchorX, s.anchorY, s.cursorX, s.cursorY
	if endY < startY || (endY == startY && endX < startX) {
		startX, startY, endX, endY = endX, endY, startX, startY
	}
	return startX, startY, endX + 1, endY, true
}

// text is what the selection copies: the rows it covers with their styling
// removed, each trimmed of the trailing blanks a terminal pads its cells with.
func (s paneSelection) text(content string, columns int) string {
	startX, startY, endX, endY, ok := s.bounds()
	if !ok {
		return ""
	}
	rows := strings.Split(content, "\n")
	if startY >= len(rows) {
		return ""
	}
	endY = min(endY, len(rows)-1)
	lines := make([]string, 0, endY-startY+1)
	for y := startY; y <= endY; y++ {
		from, to := 0, columns
		if y == startY {
			from = startX
		}
		if y == endY {
			to = endX
		}
		lines = append(lines, strings.TrimRight(ansi.Strip(ansi.Cut(rows[y], from, to)), " "))
	}
	return strings.Join(lines, "\n")
}

// highlight redraws content with the selected cells in the selection colour.
// The original styling inside the selection is dropped: a highlight that some
// cells' own colours punch through does not read as one region.
func (s paneSelection) highlight(content string, columns int) string {
	startX, startY, endX, endY, ok := s.bounds()
	if !ok {
		return content
	}
	rows := strings.Split(content, "\n")
	if startY >= len(rows) {
		return content
	}
	endY = min(endY, len(rows)-1)
	for y := startY; y <= endY; y++ {
		from, to := 0, columns
		if y == startY {
			from = startX
		}
		if y == endY {
			to = endX
		}
		rows[y] = highlightRange(rows[y], from, min(to, columns), columns)
	}
	return strings.Join(rows, "\n")
}

// highlightRange styles one row's columns [from, to). A row is padded out to
// the range it needs, because a terminal row ends where its text does and the
// user can select past that.
func highlightRange(row string, from, to, columns int) string {
	if to <= from || from >= columns {
		return row
	}
	head := pad(ansi.Cut(row, 0, from), from)
	body := pad(ansi.Strip(ansi.Cut(row, from, to)), to-from)
	return head + selectionStyle.Render(body) + ansi.TruncateLeft(row, to, "")
}

func pad(s string, width int) string {
	if gap := width - ansi.StringWidth(s); gap > 0 {
		return s + strings.Repeat(" ", gap)
	}
	return s
}

// selectable reports whether a pane is one this selection applies to. Document
// and review panes carry their own selection, and a pane whose program asked
// for the mouse gets the events instead, unless shift is held: that is the
// long-standing way to reach a terminal's own selection past an application
// that has taken the mouse over.
func (m Model) selectable(p *embeddedTerminal, shift bool) bool {
	if p == nil || p.editor != nil || p.review != nil {
		return false
	}
	return shift || p.view == nil || !p.view.mouseEnabled()
}

// paneContentAt maps a screen position to a pane and the cell under it,
// clamped to the pane so a drag that leaves the pane still selects to its edge.
func (m Model) paneContentAt(x, y int) (*embeddedTerminal, int, int, bool) {
	for _, r := range m.paneRects() {
		if x >= r.x && x < r.x+r.width && y >= r.y && y < r.y+r.height {
			columns, rows := max(1, r.width-4), max(1, r.height-2)
			return r.terminal, min(columns-1, max(0, x-r.x-2)), min(rows-1, max(0, y-r.y-1)), true
		}
	}
	return nil, 0, 0, false
}

// clearSelections drops every pane's selection, for the changes that move the
// cells out from under it: a resize, a new layout, or a keystroke.
func (m Model) clearSelections() {
	for _, p := range m.visiblePanes() {
		p.selection.clear()
	}
}

// extendSelection follows the pointer during a drag and reports whether it
// consumed the movement, so a pane being selected never also sees the motion
// as something to forward.
func (m Model) extendSelection(mouse tea.Mouse) bool {
	for _, p := range m.visiblePanes() {
		if !p.selection.dragging {
			continue
		}
		if mouse.Button != tea.MouseLeft {
			p.selection.dragging = false
			return false
		}
		_, x, y, ok := m.paneContentAt(mouse.X, mouse.Y)
		if !ok {
			return true
		}
		return p.selection.extend(x, y)
	}
	return false
}

// finishSelection ends a drag and puts what it covers on the system clipboard.
// It reports whether it handled the release, and leaves the highlight up so
// the user can see what was copied.
func (m *Model) finishSelection() (tea.Cmd, bool) {
	for _, p := range m.visiblePanes() {
		if !p.selection.dragging {
			continue
		}
		p.selection.dragging = false
		// A click that never moved is a click, not a selection: copying the
		// one character under it would cost the user their clipboard.
		if p.selection.anchorX == p.selection.cursorX && p.selection.anchorY == p.selection.cursorY {
			p.selection.clear()
			return nil, true
		}
		columns, rows := 0, 0
		for _, r := range m.paneRects() {
			if r.terminal == p {
				columns, rows = max(1, r.width-4), max(1, r.height-2)
			}
		}
		text := p.selection.text(fitPane(p.emulator.Render(), columns, rows), columns)
		if strings.TrimSpace(text) == "" {
			p.selection.clear()
			return nil, true
		}
		lines := len(strings.Split(text, "\n"))
		m.notice = fmt.Sprintf("Copied %d %s", lines, plural("line", lines))
		return tea.SetClipboard(text), true
	}
	return nil, false
}

func plural(word string, n int) string {
	if n == 1 {
		return word
	}
	return word + "s"
}
