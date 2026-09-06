package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// A help line only reaches people who read help lines. Right-clicking a pane
// is how everyone else finds out what a pane can do, and it costs them nothing
// to try.
//
// The menu offers the pane actions, and only the ones that would work on the
// pane under the pointer: an entry that does nothing teaches the wrong thing
// about the whole menu.

// menuItem is one line of the menu. The key is shown beside it, so the menu
// doubles as the thing that teaches the keyboard.
type menuItem struct {
	label  string
	action Action
}

// paneMenu is an open right-click menu, positioned where the click landed.
type paneMenu struct {
	pane  *embeddedTerminal
	items []menuItem
	at    int
	x, y  int
}

// menuFor builds the menu for a pane, leaving out what would not work on it.
func (m Model) menuFor(pane *embeddedTerminal) []menuItem {
	items := []menuItem{
		{"Split right", ActionSplitRight},
		{"Split down", ActionSplitDown},
	}
	// Zoom and cycling need somewhere to go.
	if len(m.visiblePanes()) > 1 {
		items = append(items,
			menuItem{"Zoom", ActionZoom},
			menuItem{"Next pane", ActionNextPane},
		)
	}
	items = append(items, menuItem{"Rename", ActionRenamePane})
	if pane.editor == nil && pane.review == nil {
		// Scrollback belongs to a daemon-owned terminal; a document pane
		// scrolls with the wheel.
		items = append(items, menuItem{"Scrollback", ActionScrollback})
		if pane.view != nil && !pane.view.controls() {
			items = append(items, menuItem{"Take control", ActionClaimPane})
		}
	}
	items = append(items, menuItem{"Open a file", ActionEditFile})
	if m.canClose(pane) {
		items = append(items, menuItem{"Close pane", ActionClosePane})
	}
	return items
}

// openMenu opens the menu over the pane under the pointer, which it also
// focuses: acting on a pane the user did not click would be a surprise.
func (m *Model) openMenu(mouse tea.Mouse) bool {
	pane, _, _, ok := m.paneContentAt(mouse.X, mouse.Y)
	if !ok || pane == nil {
		return false
	}
	m.embedded = pane
	m.sidebarFocused = false
	m.filesFocused = false
	m.menu = &paneMenu{pane: pane, items: m.menuFor(pane), x: mouse.X, y: mouse.Y}
	return true
}

// updateMenu handles the keyboard while the menu is open. It owns every key,
// so a stray letter closes it rather than reaching the pane behind it.
func (m Model) updateMenu(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc", "q":
		m.menu = nil
		return m, nil
	case "up", "k":
		if m.menu.at > 0 {
			m.menu.at--
		}
		return m, nil
	case "down", "j":
		if m.menu.at+1 < len(m.menu.items) {
			m.menu.at++
		}
		return m, nil
	case "enter":
		return m.chooseMenuItem(m.menu.at)
	}
	m.menu = nil
	return m, nil
}

// chooseMenuItem runs an entry through the same path the keyboard uses, so a
// menu entry and its key cannot drift apart.
func (m Model) chooseMenuItem(index int) (tea.Model, tea.Cmd) {
	if m.menu == nil || index < 0 || index >= len(m.menu.items) {
		m.menu = nil
		return m, nil
	}
	action := m.menu.items[index].action
	m.menu = nil
	cmd, _ := m.paneAction(m.keys.key(action))
	return m, cmd
}

// menuClick routes a click while the menu is open: on an entry it runs it,
// anywhere else it closes the menu without doing anything, which is what
// clicking away from a menu means everywhere else.
func (m Model) menuClick(mouse tea.Mouse) (tea.Model, tea.Cmd) {
	x, y, width, _ := m.menuBounds()
	row := mouse.Y - y - 1
	if mouse.X >= x && mouse.X < x+width && row >= 0 && row < len(m.menu.items) {
		return m.chooseMenuItem(row)
	}
	m.menu = nil
	return m, nil
}

// menuBox renders the menu itself. The box is built first and measured after,
// rather than its size being predicted: padding and a border belong to the
// styling, and guessing at them is how a label and its key end up wrapped onto
// two lines.
func (m Model) menuBox() string {
	inner := 0
	for _, item := range m.menu.items {
		if length := len(item.label) + len(m.keys.key(item.action)) + 2; length > inner {
			inner = length
		}
	}
	lines := make([]string, 0, len(m.menu.items))
	for index, item := range m.menu.items {
		key := m.keys.key(item.action)
		gap := max(1, inner-len(item.label)-len(key))
		text := item.label + strings.Repeat(" ", gap) + key
		if index == m.menu.at {
			text = selectedStyle.Render(text)
		}
		lines = append(lines, text)
	}
	// No explicit width: every line is already padded to the same length, so
	// the styling sizes the box to its contents rather than reflowing them.
	return panelStyle.Render(strings.Join(lines, "\n"))
}

// menuBounds is where the menu is drawn, nudged so it stays on screen when it
// was opened near an edge.
func (m Model) menuBounds() (x, y, width, height int) {
	box := m.menuBox()
	width, height = lipgloss.Width(box), lipgloss.Height(box)
	x, y = m.menu.x, m.menu.y
	if x+width > m.width {
		x = max(0, m.width-width)
	}
	if y+height > m.height {
		y = max(0, m.height-height)
	}
	return x, y, width, height
}

// renderMenu draws the menu over the frame it was opened on.
func (m Model) renderMenu(frame string) string {
	if m.menu == nil {
		return frame
	}
	x, y, _, _ := m.menuBounds()
	return overlay(frame, m.menuBox(), x, y)
}

// overlay draws a box over a frame at a position, leaving the rest alone. The
// frame is already styled, so the box is written across it row by row rather
// than by rebuilding the layout.
func overlay(frame, box string, x, y int) string {
	rows := strings.Split(frame, "\n")
	for index, line := range strings.Split(box, "\n") {
		row := y + index
		if row < 0 || row >= len(rows) {
			continue
		}
		width := lipgloss.Width(line)
		left := pad(ansi.Cut(rows[row], 0, x), x)
		right := ansi.TruncateLeft(rows[row], x+width, "")
		rows[row] = left + line + right
	}
	return strings.Join(rows, "\n")
}
