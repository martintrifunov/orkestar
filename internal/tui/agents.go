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
