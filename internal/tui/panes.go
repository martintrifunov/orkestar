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

func (m Model) visiblePanes() []*embeddedTerminal {
	if len(m.panes) > 0 {
		return m.panes
	}
	if m.embedded != nil {
		return []*embeddedTerminal{m.embedded}
	}
	return nil
}
func (m Model) paneRects() []paneRect {
	panes := m.visiblePanes()
	if len(panes) == 0 {
		return nil
	}
	x := embeddedSidebarWidth(m.width) + 1
	w := m.width - x
	h := m.height - 3
	columns := len(panes)
	rows := 1
	if m.stacked {
		columns = 1
		rows = len(panes)
	} else if len(panes) > 2 {
		columns = 2
		rows = (len(panes) + 1) / 2
	}
	if w/columns < 24 || h/rows < 7 {
		panes = []*embeddedTerminal{m.embedded}
		columns = 1
		rows = 1
	}
	var rects []paneRect
	for i, p := range panes {
		col, row := i%columns, i/columns
		left := x + col*w/columns
		top := 2 + row*h/rows
		right := x + (col+1)*w/columns
		bottom := 2 + (row+1)*h/rows
		rects = append(rects, paneRect{p, left, top, right - left, bottom - top})
	}
	return rects
}
func (m Model) resizePanes() {
	for _, r := range m.paneRects() {
		sendEmbeddedResize(r.terminal, max(1, r.width-4), max(1, r.height-2))
	}
}
func (m *Model) addPane(p *embeddedTerminal) {
	if len(m.panes) == 0 && m.embedded != nil {
		m.panes = []*embeddedTerminal{m.embedded}
	}
	if len(m.panes) >= 4 {
		if !m.canClose(m.embedded) {
			p.close()
			return
		}
		m.removePane(m.embedded)
	}
	m.panes = append(m.panes, p)
	m.embedded = p
	m.sidebarFocused = false
	m.resizePanes()
}
func (m *Model) removePane(p *embeddedTerminal) {
	p.close()
	var panes []*embeddedTerminal
	for _, existing := range m.visiblePanes() {
		if existing != p {
			panes = append(panes, existing)
		}
	}
	m.panes = panes
	m.embedded = nil
	if len(panes) > 0 {
		m.embedded = panes[len(panes)-1]
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
	var rows []string
	var row []string
	lastY := rects[0].y
	for _, r := range rects {
		if r.y != lastY {
			rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Top, row...))
			row = nil
			lastY = r.y
		}
		style := panelStyle
		if r.terminal == m.embedded && !m.sidebarFocused {
			style = style.BorderForeground(lipgloss.Color("#D7A84B"))
		}
		content := r.terminal.emulator.Render()
		row = append(row, style.Width(r.width).Height(r.height).Render(fitPane(content, r.width-4, r.height-2)))
	}
	rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Top, row...))
	return strings.Join(rows, "\n")
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
