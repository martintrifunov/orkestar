package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/martintrifunov/orkestar/internal/ipc"
)

type paneRect struct {
	terminal            *embeddedTerminal
	x, y, width, height int
}

// tree returns the authoritative layout, or a single synthesized leaf when a
// pane was attached without going through insertPane.
func (m Model) tree() *splitNode {
	if m.layout != nil {
		return m.layout
	}
	if m.embedded != nil {
		return &splitNode{pane: m.embedded}
	}
	return nil
}
func (m Model) visiblePanes() []*embeddedTerminal {
	return m.tree().leaves(nil)
}
func (m Model) contentArea() (x, y, width, height int) {
	x = embeddedSidebarWidth(m.width) + 1
	return x, 2, m.width - x, m.height - 3
}

// paneRects derives every pane box from the split tree. When any box would be
// too small to use, only the focused pane is shown at full size; hidden panes
// keep their attachments and F6 still cycles through them.
func (m Model) paneRects() []paneRect {
	tree := m.tree()
	if tree == nil {
		return nil
	}
	x, y, w, h := m.contentArea()
	rects := tree.rects(x, y, w, h, nil)
	for _, r := range rects {
		if r.width < minPaneWidth || r.height < minPaneHeight {
			return []paneRect{{m.embedded, x, y, w, h}}
		}
	}
	return rects
}
func (m Model) resizePanes() {
	for _, r := range m.paneRects() {
		sendEmbeddedResize(r.terminal, max(1, r.width-4), max(1, r.height-2))
	}
}

// autoStacked picks the orientation for a pane inserted without an explicit
// split key: split the target along its longer visual edge, treating a cell
// as roughly twice as tall as it is wide.
func (m Model) autoStacked(target *embeddedTerminal) bool {
	x, y, w, h := m.contentArea()
	for _, r := range m.tree().rects(x, y, w, h, nil) {
		if r.terminal == target {
			return r.width < 2*r.height
		}
	}
	return w < 2*h
}

// addPane inserts p beside the focused pane. Callers check roomForPane first;
// a pane is never replaced silently.
func (m *Model) addPane(p *embeddedTerminal) {
	m.insertPane(p, m.embedded, m.autoStacked(m.embedded))
}

// insertPane places p beside or below target and focuses it. A target that is
// no longer open falls back to the focused pane.
func (m *Model) insertPane(p, target *embeddedTerminal, stacked bool) {
	if m.layout == nil && m.embedded != nil {
		m.layout = &splitNode{pane: m.embedded}
	}
	if leaf, _ := m.layout.find(target, nil); leaf == nil {
		target = m.embedded
	}
	m.layout = m.layout.insert(target, p, stacked)
	m.embedded = p
	m.sidebarFocused = false
	m.resizePanes()
}

// removePane closes p's attachment and collapses its split. Focus moves to the
// pane that inherits its space unless another pane was focused.
func (m *Model) removePane(p *embeddedTerminal) {
	p.close()
	tree := m.tree()
	next := m.embedded
	if next == p {
		next = nil
		if heirs := tree.sibling(p); len(heirs) > 0 {
			next = heirs[0]
		}
	}
	m.layout = tree.remove(p)
	m.embedded = next
	if m.embedded == nil {
		if leaves := m.layout.leaves(nil); len(leaves) > 0 {
			m.embedded = leaves[len(leaves)-1]
		}
	}
	m.resizePanes()
}
func (m Model) closePanes() {
	for _, p := range m.visiblePanes() {
		p.close()
	}
}
func (m Model) findPane(id string) *embeddedTerminal {
	for _, p := range m.visiblePanes() {
		if p.terminalID == id {
			return p
		}
	}
	return nil
}
func (m *Model) nextPane() {
	panes := m.visiblePanes()
	for i, p := range panes {
		if p == m.embedded {
			m.embedded = panes[(i+1)%len(panes)]
			m.sidebarFocused = false
			m.resizePanes()
			return
		}
	}
}
func (m Model) renderPanes() string {
	rects := m.paneRects()
	if len(rects) == 0 {
		return ""
	}
	boxes := map[*embeddedTerminal]string{}
	for _, r := range rects {
		style := panelStyle
		if r.terminal == m.embedded && !m.sidebarFocused {
			style = style.BorderForeground(lipgloss.Color("#D7A84B"))
		}
		content := r.terminal.emulator.Render()
		boxes[r.terminal] = style.Width(r.width).Height(r.height).Render(fitPane(content, r.width-4, r.height-2))
	}
	if len(rects) == 1 {
		return boxes[rects[0].terminal]
	}
	return m.tree().render(boxes)
}
func (m Model) mouseClick(msg tea.MouseClickMsg) (tea.Model, tea.Cmd) {
	if m.pickingAgent || m.viewingDiff || m.viewingHistory || m.filePrompt || m.settingsOpen {
		return m, nil
	}
	mouse := msg.Mouse()
	for _, r := range m.paneRects() {
		if mouse.X >= r.x && mouse.X < r.x+r.width && mouse.Y >= r.y && mouse.Y < r.y+r.height {
			m.embedded = r.terminal
			m.sidebarFocused = false
			m.forwardMouse("click", mouse)
			if e := r.terminal.editor; e != nil && mouse.Button == tea.MouseLeft {
				e.click(mouse.X-r.x-2, mouse.Y-r.y-1, mouse.Mod&tea.ModShift != 0)
				e.dragging = true
			}
			if review := r.terminal.review; review != nil && mouse.Button == tea.MouseLeft {
				row := mouse.Y - r.y - 2
				if row >= 0 && row < min(3, len(review.files)) {
					return m, m.refreshReview(r.terminal, min(len(review.files)-1, max(0, review.selected-1)+row))
				}
			}
			return m, nil
		}
	}
	if mouse.X < embeddedSidebarWidth(m.width) {
		m.sidebarFocused = true
		lines := strings.Split(ansi.Strip(m.renderSidebar(embeddedSidebarWidth(m.width)-4, max(1, m.height-5))), "\n")
		at := mouse.Y - 3
		section := focusSessions
		for y, line := range lines {
			title := strings.TrimSpace(strings.TrimPrefix(line, "› "))
			switch title {
			case "Sessions":
				section = focusSessions
			case "Tasks":
				section = focusTasks
			case "Agents":
				section = focusAgents
			}
			if y == at {
				m.focus = section
				break
			}
		}
	}
	return m, nil
}
func (m Model) resumeSelected() tea.Cmd {
	if m.opening || m.focus != focusAgents || m.agentSelected >= len(m.snapshot.Agents) {
		return nil
	}
	id := m.snapshot.Agents[m.agentSelected].ID
	return func() tea.Msg {
		var result agentLaunchedMsg
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		result.err = m.client.Call(ctx, "agent.resume", map[string]string{"agent_id": id}, &result.agent)
		return result
	}
}
func (m Model) claimPane() {
	if m.embedded != nil && m.embedded.stream != nil {
		_ = m.embedded.stream.Send(map[string]any{"version": ipc.Version, "command": "claim"})
		// The daemon accepts this resize only if the preceding claim succeeded.
		for _, r := range m.paneRects() {
			if r.terminal == m.embedded {
				_ = m.embedded.stream.Send(resizeCommand(max(1, r.width-4), max(1, r.height-2)))
			}
		}
	}
}
func (m Model) paneTitle() string {
	if m.embedded == nil {
		return ""
	}
	name := m.embedded.terminalID
	if m.embedded.title != "" {
		name = m.embedded.title
	}
	if m.embedded.editor != nil {
		name = m.embedded.editor.title()
	}
	label := fmt.Sprintf("  %d panes · %s", len(m.visiblePanes()), name)
	if len(m.paneRects()) < len(m.visiblePanes()) {
		label += " · enlarge window for splits"
	}
	if m.embedded.view != nil && !m.embedded.view.controls() {
		label += " · viewing (ctrl+b t to claim)"
	}
	return label
}

func (m Model) forwardMouse(kind string, mouse tea.Mouse) bool {
	if m.embedded == nil || m.embedded.view == nil || !m.embedded.view.mouseEnabled() || m.filePrompt || m.settingsOpen || m.viewingHistory || m.viewingDiff || m.pickingAgent {
		return false
	}
	for _, r := range m.paneRects() {
		if r.terminal == m.embedded && (kind == "release" || mouse.X >= r.x+2 && mouse.X < r.x+r.width-2 && mouse.Y >= r.y+1 && mouse.Y < r.y+r.height-1) {
			mouse.X = max(r.x+2, min(r.x+r.width-3, mouse.X))
			mouse.Y = max(r.y+1, min(r.y+r.height-2, mouse.Y))
			_ = r.terminal.stream.Send(map[string]any{"version": ipc.Version, "command": "mouse", "kind": kind, "x": mouse.X - r.x - 2, "y": mouse.Y - r.y - 1, "button": int(mouse.Button), "modifiers": int(mouse.Mod)})
			return true
		}
	}
	return false
}
