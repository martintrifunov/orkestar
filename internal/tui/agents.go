package tui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/martintrifunov/orkestar/internal/agent"
)

// adaptersReloadedMsg reports the adapter set after a manifest reload. A
// manifest edited on disk only took effect after `orkestar agent reload`; the
// settings screen now offers the same action.
type adaptersReloadedMsg struct {
	adapters  []agent.Capabilities
	err       error
	machineID string
}

// openRemoteAgent shows an agent on another machine by selecting that machine
// first, so the pane attaches through the right daemon. The switch refuses
// when a pane here cannot close, and its notice says why.
func (m *Model) openRemoteAgent(scoped scopedAgent) tea.Cmd {
	index := -1
	for i, candidate := range m.machines {
		if candidate.ID == scoped.MachineID {
			index = i
			break
		}
	}
	if index < 0 {
		m.notice = "That machine is no longer saved."
		return nil
	}
	if index == m.machineIndex {
		if id := scoped.Agent.TerminalID; id != "" {
			m.opening = true
			return m.openTerminal(id)
		}
		m.notice = "That agent has no terminal to open."
		return nil
	}
	cmd := m.switchMachine(index - m.machineIndex)
	if cmd == nil {
		return nil
	}
	if scoped.Agent.TerminalID == "" {
		return cmd
	}
	m.opening = true
	return tea.Batch(cmd, m.openTerminal(scoped.Agent.TerminalID))
}

func (m Model) reloadAdapters() tea.Cmd {
	client := m.client
	machineID := m.currentMachine().ID
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		var adapters []agent.Capabilities
		err := client.Call(ctx, "agent.reloadAdapters", nil, &adapters)
		return adaptersReloadedMsg{adapters: adapters, err: err, machineID: machineID}
	}
}
