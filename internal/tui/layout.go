package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// fitPane clips styled lines instead of wrapping them into adjacent panes.
// Tabs are replaced because the outer terminal would expand them beyond the
// measured width and wrap the row.
func fitPane(content string, columns, rows int) string {
	lines := strings.Split(strings.ReplaceAll(content, "\t", " "), "\n")
	if len(lines) > rows {
		lines = lines[:max(0, rows)]
	}
	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], max(0, columns), "")
	}
	return strings.Join(lines, "\n")
}

func (m Model) renderEmbedded(width, height int) string {
	if width < 50 || height < 16 {
		return fitPane("Orkestar\nEnlarge terminal to at least 50 × 16.\nWork continues in the daemon.", width, height)
	}
	_, _, contentWidth, contentHeight := m.contentArea()
	columns, rows := contentWidth-4, contentHeight-2
	sidebarWidth := embeddedSidebarWidth(width)
	sidebar := panelStyle.Width(sidebarWidth).Height(rows + 2).
		Render(m.renderSidebar(sidebarWidth-4, rows))
	content := "Open a session\n\nSelect a session or agent and press Enter.\nPress a to launch an agent, or n for a shell."
	if m.embedded != nil {
		content = m.embedded.emulator.Render()
	}
	if m.viewingDiff {
		content = m.renderDiff(columns)
	}
	if m.viewingHistory {
		content = m.renderHistory(rows)
	}
	if m.prompting() {
		content = m.promptView()
	}
	if m.pickingAgent {
		content = m.renderAgentPicker(columns)
	}
	paneStyle := panelStyle
	if m.embedded != nil && !m.sidebarFocused && !m.filesFocused {
		paneStyle = paneStyle.BorderForeground(lipgloss.Color("#D7A84B"))
	}
	pane := paneStyle.Width(columns + 4).Height(rows + 2).Render(fitPane(content, columns, rows))
	if m.embedded != nil && !m.pickingAgent && !m.viewingDiff && !m.viewingHistory && !m.prompting() {
		pane = m.renderPanes()
	}
	title := "  persistent agent runtime"
	if m.embedded != nil {
		title = m.paneTitle()
	}
	if m.notice != "" {
		title = "  " + m.notice
	}
	if m.opening {
		title += "  opening…"
	}
	if m.err != nil {
		title = "  " + errorStyle.Render(m.err.Error())
	}
	header := ansi.Truncate(accentStyle.Render("Orkestar")+dimStyle.Render(title), width, "…")
	help := "a agent  n shell  enter open  X stop/remove  f files  tab section  q quit"
	if m.embedded != nil && !m.sidebarFocused {
		help = "Ctrl+b then: v/s split · o next · d diff · e edit · f files · [ scrollback · q close"
	}
	if m.embedded != nil && m.sidebarFocused {
		help = "enter open  X stop/remove  a agent  n shell  f files  esc terminal  q quit"
	}
	if m.focus == focusTasks && (m.embedded == nil || m.sidebarFocused) {
		help = "c new  d diff  m done  x cancel  w worktree  t assign  tab section"
	}
	if m.focus == focusAgents && (m.embedded == nil || m.sidebarFocused) {
		help = "enter open  u resume  i interrupt  X stop/remove  y/x allow/deny  tab section"
	}
	if m.pickingAgent {
		help = "up/down select  enter launch  esc cancel"
	}
	if m.viewingHistory {
		help = "Scrollback · pgup/pgdown or wheel · esc return"
	}
	if len(m.paneRects()) < len(m.visiblePanes()) {
		help = "Enlarge window for splits · F6 cycles hidden panes"
	}
	if m.prefix {
		help = "Prefix: v/s split · o next · d diff · e edit · f files · [ scrollback · , settings · q close"
	}
	if m.filesFocused {
		help = "↑/↓ select · enter open · ←/→ collapse/expand · r refresh · esc back"
	}
	if m.prompting() {
		help = "Enter confirm · Esc cancel"
	}
	if m.viewingDiff {
		help = "esc close diff"
	}
	body := []string{sidebar, " ", pane}
	if files := m.renderFiles(); files != "" {
		body = append(body, " ", files)
	}
	return header + "\n\n" + lipgloss.JoinHorizontal(lipgloss.Top, body...) + "\n" + ansi.Truncate(dimStyle.Render(help), width, "…")
}

func (m Model) renderSidebar(columns, rows int) string {
	sections := []string{m.renderWorkspaces(), m.renderTerminals(), m.renderTasks(), m.renderAgents()}
	heights := make([]int, len(sections))
	total := len(sections) - 1
	for i, section := range sections {
		heights[i] = len(strings.Split(section, "\n"))
		total += heights[i]
	}
	// Share scarce rows across sections while retaining every section heading.
	for total > rows {
		largest := 0
		for i := range heights {
			if heights[i] > heights[largest] {
				largest = i
			}
		}
		if heights[largest] <= 2 {
			break
		}
		heights[largest]--
		total--
	}
	selected := []int{0, m.selected * 2, 0, 0}
	for i, task := range m.snapshot.Tasks {
		if i >= m.taskSelected {
			break
		}
		selected[2]++
		if task.WorktreePath != "" {
			selected[2]++
		}
	}
	for i, agent := range m.snapshot.Agents {
		if i >= m.agentSelected {
			break
		}
		selected[3]++
		if agent.AttentionReason != "" {
			selected[3]++
		}
	}
	for i, section := range sections {
		lines := strings.Split(section, "\n")
		if len(lines) > heights[i] {
			count := heights[i] - 1
			start := min(max(0, selected[i]-count+1), len(lines)-1-count)
			lines = append([]string{lines[0]}, lines[1+start:1+start+count]...)
		}
		if (i == 1 && m.focus == focusSessions || i == 2 && m.focus == focusTasks || i == 3 && m.focus == focusAgents) && (m.embedded == nil || m.sidebarFocused) {
			lines[0] = accentStyle.Render("› ") + lines[0]
		}
		sections[i] = fitPane(strings.Join(lines, "\n"), columns, heights[i])
	}
	return fitPane(strings.Join(sections, "\n\n"), columns, rows)
}
