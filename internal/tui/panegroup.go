package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/martintrifunov/orkestar/internal/workflow"
)

// Panes already belong to tasks — an agent is launched for one and its
// terminal is the pane — but nothing showed it. Three claude panes looked
// identical, the Tasks list never said which of its work was on screen, and
// there was no way to get from a task to the pane doing it.
//
// This is the grouping other multiplexers get from arbitrary tabs, taken from
// the structure Orkestar already models rather than a new one laid beside it.

// taskOfPane returns the task a pane is working, if it is an agent's terminal
// and that agent was launched for one.
func (m Model) taskOfPane(pane *embeddedTerminal) (workflow.Task, bool) {
	if pane == nil || pane.terminalID == "" {
		return workflow.Task{}, false
	}
	for _, agent := range m.snapshot.Agents {
		if agent.TerminalID != pane.terminalID || agent.TaskID == "" {
			continue
		}
		for _, task := range m.snapshot.Tasks {
			if task.ID == agent.TaskID {
				return task, true
			}
		}
	}
	return workflow.Task{}, false
}

// panesForTask returns the open panes working a task, in layout order.
func (m Model) panesForTask(taskID string) []*embeddedTerminal {
	if taskID == "" {
		return nil
	}
	var group []*embeddedTerminal
	for _, pane := range m.visiblePanes() {
		if task, ok := m.taskOfPane(pane); ok && task.ID == taskID {
			group = append(group, pane)
		}
	}
	return group
}

// terminalForTask is the terminal an assigned agent is running in, which is
// what "show me this task" opens.
func (m Model) terminalForTask(taskID string) string {
	if taskID == "" {
		return ""
	}
	for _, agent := range m.snapshot.Agents {
		if agent.TaskID == taskID && agent.TerminalID != "" {
			return agent.TerminalID
		}
	}
	return ""
}

// focusOrOpen brings a terminal's pane to focus, opening one only if it is not
// already on screen. Opening again would attach a second pane to the same
// terminal, which is legal and never what someone jumping to a task meant.
func (m *Model) focusOrOpen(terminalID string) tea.Cmd {
	if terminalID == "" {
		return nil
	}
	if pane := m.findPane(terminalID); pane != nil {
		m.embedded = pane
		m.sidebarFocused = false
		m.filesFocused = false
		return nil
	}
	if m.opening || !m.roomForPane() {
		return nil
	}
	m.opening = true
	return m.openTerminal(terminalID)
}

// openSelectedTask shows the work a task is having done: its agent's pane,
// focused if it is already open.
func (m *Model) openSelectedTask() tea.Cmd {
	task, ok := m.selectedTask()
	if !ok {
		return nil
	}
	if group := m.panesForTask(task.ID); len(group) > 0 {
		// Already on screen, so cycle within the task rather than re-focusing
		// the same pane: this is the one place a task's panes are a group.
		m.embedded = nextInGroup(group, m.embedded)
		m.sidebarFocused = false
		m.filesFocused = false
		return nil
	}
	if terminalID := m.terminalForTask(task.ID); terminalID != "" {
		return m.focusOrOpen(terminalID)
	}
	m.notice = "Nothing is working this task yet. Press a to start an agent on it."
	return nil
}

// nextInGroup returns the pane after current within group, wrapping, or the
// first when current is not in it.
func nextInGroup(group []*embeddedTerminal, current *embeddedTerminal) *embeddedTerminal {
	for index, pane := range group {
		if pane == current {
			return group[(index+1)%len(group)]
		}
	}
	return group[0]
}
