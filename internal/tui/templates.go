package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/martintrifunov/orkestar/internal/daemon"
	"github.com/martintrifunov/orkestar/internal/workflow"
)

// templatesMsg carries a workspace's template list back to the picker, along
// with the workspace the list was read from so applying does not have to
// resolve it a second time.
type templatesMsg struct {
	templates   []workflow.Template
	workspaceID string
	err         error
	machineID   string
}

// templateAppliedMsg reports what applying a template produced.
type templateAppliedMsg struct {
	applied   daemon.AppliedTemplate
	err       error
	machineID string
}

// loadTemplates reads the workspace's template directory. Workspace creation
// is part of the same call, so a directory with templates but no board yet
// still lists them.
func (m Model) loadTemplates() tea.Cmd {
	machineID := m.currentMachine().ID
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		workspaceID, err := m.ensureWorkspace(ctx)
		if err != nil {
			return templatesMsg{err: err, machineID: machineID}
		}
		var listed struct {
			Templates []workflow.Template `json:"templates"`
		}
		err = m.client.Call(ctx, "template.list", map[string]any{"workspace_id": workspaceID}, &listed)
		return templatesMsg{templates: listed.Templates, workspaceID: workspaceID, err: err, machineID: machineID}
	}
}

// applyTemplate creates the template's tasks, and starts the agents it names
// when asked. Applying can take a while: a template task may need a worktree,
// and a launch waits for the agent's own startup signal.
func (m Model) applyTemplate(workspaceID, name string, start bool) tea.Cmd {
	client := m.client
	machineID := m.currentMachine().ID
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		var applied daemon.AppliedTemplate
		err := client.Call(ctx, "template.apply", map[string]any{
			"workspace_id": workspaceID, "name": name, "start": start,
		}, &applied)
		return templateAppliedMsg{applied: applied, err: err, machineID: machineID}
	}
}

// appliedNotice summarizes an apply the way the CLI does: what now exists,
// what started, and what is still waiting on a dependency.
func appliedNotice(applied daemon.AppliedTemplate) string {
	notice := fmt.Sprintf("Template %s: %d tasks", applied.Template, len(applied.Tasks))
	if len(applied.Agents) > 0 {
		notice += fmt.Sprintf(", %d agents started", len(applied.Agents))
	}
	if len(applied.Waiting) > 0 {
		notice += fmt.Sprintf(", %d waiting on dependencies", len(applied.Waiting))
	}
	if applied.Failed != "" {
		notice += " — " + applied.Failed
	}
	return notice
}

func (m Model) renderTemplatePicker(width int) string {
	header := m.theme.accent.Render("Apply template") +
		m.theme.dim.Render("  creates its tasks on this board")
	if m.templatesErr != nil {
		return header + "\n\n" + m.theme.error.Render(m.templatesErr.Error()) +
			"\n\n" + m.theme.dim.Render("esc back")
	}

	var lines []string
	switch {
	case m.templatesLoading:
		lines = append(lines, m.theme.dim.Render("Reading templates…"))
	case len(m.templates) == 0:
		lines = append(lines, m.theme.dim.Render("No templates. Add .orkestar/templates/<name>.json to the workspace."))
	}
	for index, template := range m.templates {
		count := fmt.Sprintf("%d tasks", len(template.Tasks))
		line := fmt.Sprintf("%-16s  %-8s  %s", template.Name, count, template.Description)
		if index == m.templateAt {
			line = m.theme.selected.Render(" " + line + " ")
		} else {
			line = "  " + line
		}
		lines = append(lines, line)
	}
	panel := m.theme.panel.Width(width - 4).Render(strings.Join(lines, "\n"))

	start := "off"
	if m.templateStart {
		start = "on"
	}
	footer := "up/down select  enter apply  s start agents: " + start + "  esc cancel"
	return header + "\n\n" + panel + "\n\n" + m.theme.dim.Render(footer)
}

// updateTemplatePicker handles the template overlay's keys. Start is off by
// default, matching the CLI: creating tasks is recoverable, while launching
// every agent a template names spends tokens the moment it is asked for.
func (m Model) updateTemplatePicker(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc", "q":
		m.pickingTemplate = false
		return m, nil
	case "up", "k":
		if m.templateAt > 0 {
			m.templateAt--
		}
	case "down", "j":
		if m.templateAt+1 < len(m.templates) {
			m.templateAt++
		}
	case "s":
		m.templateStart = !m.templateStart
	case "enter":
		if m.taskBusy || m.templatesErr != nil || len(m.templates) == 0 ||
			m.templateAt < 0 || m.templateAt >= len(m.templates) {
			return m, nil
		}
		template := m.templates[m.templateAt]
		m.pickingTemplate = false
		m.taskBusy = true
		m.notice = "Applying " + template.Name + "…"
		return m, m.applyTemplate(m.templatesWorkspace, template.Name, m.templateStart)
	}
	return m, nil
}
