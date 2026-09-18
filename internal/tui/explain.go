package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/martintrifunov/orkestar/internal/daemon"
)

// explainMsg carries the daemon's account of why an agent is in the state the
// sidebar shows. agent.explain has been on the IPC and CLI surfaces since
// 0.5.0; this is the first place a person can read it without leaving the UI.
type explainMsg struct {
	explanation daemon.AgentExplanation
	err         error
	machineID   string
}

// loadExplanation asks the highlighted agent's own daemon why it is in the
// state the sidebar shows. It returns nothing when no agent is selected.
func (m *Model) loadExplanation() tea.Cmd {
	scoped, ok := m.scopedAgentAt(m.agentSelected)
	if !ok {
		return nil
	}
	machine, ok := m.agentMachine(scoped)
	if !ok {
		return nil
	}
	m.viewingExplanation = true
	m.explanation = daemon.AgentExplanation{}
	m.explainErr = nil
	// The reply is accepted only from the machine the overlay was opened for,
	// so a switch while it is in flight cannot put one machine's answer under
	// another's name.
	m.explainMachine = machine.ID
	m.explainTaskTitle = m.taskTitleOfScoped(scoped)
	client := machine.Client
	agentID := scoped.Agent.ID
	machineID := machine.ID
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var explanation daemon.AgentExplanation
		err := client.Call(ctx, "agent.explain", map[string]string{"agent_id": agentID}, &explanation)
		return explainMsg{explanation: explanation, err: err, machineID: machineID}
	}
}

func (m Model) renderExplanation(width int) string {
	header := m.theme.accent.Render("Agent explanation") +
		m.theme.dim.Render("  why Orkestar believes what the sidebar reports")
	footer := m.theme.dim.Render("esc/q back")
	if m.explainErr != nil {
		return header + "\n\n" + m.theme.error.Render(m.explainErr.Error()) + "\n\n" + footer
	}
	agent := m.explanation.Agent
	if agent.ID == "" {
		return header + "\n\n" + m.theme.dim.Render("Reading…") + "\n\n" + footer
	}

	yesNo := func(value bool) string {
		if value {
			return "yes"
		}
		return "no"
	}
	signal := agent.SignalSource
	if signal == "" {
		signal = "process only"
	}
	lines := []string{
		m.theme.accent.Render("State"),
		fmt.Sprintf("  %s · %s · %s", agent.Adapter, agent.Mode, agent.State),
		fmt.Sprintf("  live process: %s   resumable: %s", yesNo(m.explanation.Live), yesNo(m.explanation.Resumable)),
	}
	if m.explainTaskTitle != "" {
		lines = append(lines, "  on "+m.explainTaskTitle)
	}
	lines = append(lines, m.theme.accent.Render("Identity"), "  id "+agent.ID)
	if agent.NativeSessionID != "" {
		lines = append(lines, "  native session "+agent.NativeSessionID)
	}
	lines = append(lines, "  signal "+signal)

	if len(m.explanation.Reasons) > 0 {
		lines = append(lines, "", m.theme.accent.Render("Why"))
		for _, reason := range m.explanation.Reasons {
			lines = append(lines, "  • "+reason)
		}
	}
	if len(m.explanation.Permissions) > 0 {
		lines = append(lines, "", m.theme.accent.Render("Waiting on you"))
		for _, permission := range m.explanation.Permissions {
			lines = append(lines, "  • "+permission.Reason)
		}
	}
	panel := m.theme.panel.Width(width - 4).Render(strings.Join(lines, "\n"))
	return header + "\n\n" + panel + "\n\n" + footer
}
