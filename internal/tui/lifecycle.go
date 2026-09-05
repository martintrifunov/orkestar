package tui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"
)

// lifecycleMsg reports a stop, removal or interrupt. removedTerminal lets the
// model close a pane that was attached to a session that no longer exists.
type lifecycleMsg struct {
	notice          string
	removedTerminal string
	err             error
}

// selectedLifecycleTarget describes what the sidebar is pointing at, so one
// key can act on either a session or an agent.
type lifecycleTarget struct {
	kind, id, terminalID, label, state string
}

func (m Model) lifecycleTarget() (lifecycleTarget, bool) {
	switch m.focus {
	case focusSessions:
		if m.selected < len(m.snapshot.Terminals) {
			t := m.snapshot.Terminals[m.selected]
			label := t.ID
			if len(t.Command) > 0 {
				label = t.Command[0]
			}
			return lifecycleTarget{"terminal", t.ID, t.ID, label, t.State}, true
		}
	case focusAgents:
		if m.agentSelected < len(m.snapshot.Agents) {
			a := m.snapshot.Agents[m.agentSelected]
			return lifecycleTarget{"agent", a.ID, a.TerminalID, a.Adapter, a.State}, true
		}
	}
	return lifecycleTarget{}, false
}

// running reports whether the target still holds a live process.
func (t lifecycleTarget) running() bool {
	switch t.state {
	case "stopped", "crashed", "interrupted", "":
		return false
	}
	return true
}

// stopOrRemoveSelected ends a live session or agent, or clears a finished one
// from the list. Stopping something that is still running takes two presses,
// because it ends work the daemon is holding independently of this UI.
// Clearing a finished record is immediate, since there is nothing to lose.
func (m *Model) stopOrRemoveSelected() tea.Cmd {
	target, ok := m.lifecycleTarget()
	if !ok {
		return nil
	}
	if target.running() {
		if m.pendingStop != target.id {
			m.pendingStop = target.id
			m.notice = "Press X again to stop " + target.label + ". It is still running."
			return nil
		}
		m.pendingStop = ""
		m.notice = "Stopping " + target.label + "…"
		method := "terminal.stop"
		params := map[string]any{"terminal_id": target.id}
		if target.kind == "agent" {
			method, params = "agent.stop", map[string]any{"agent_id": target.id}
		}
		return m.lifecycleCall(method, params, "Stopped "+target.label, "")
	}
	m.pendingStop = ""
	method := "terminal.remove"
	params := map[string]any{"terminal_id": target.id}
	if target.kind == "agent" {
		method, params = "agent.remove", map[string]any{"agent_id": target.id}
	}
	return m.lifecycleCall(method, params, "Removed "+target.label, target.terminalID)
}

// interruptSelected stops an agent's current turn without ending the session,
// the same thing Ctrl+C does when typed into its terminal.
func (m *Model) interruptSelected() tea.Cmd {
	target, ok := m.lifecycleTarget()
	if !ok || target.kind != "agent" {
		return nil
	}
	if !target.running() {
		m.notice = target.label + " is not running."
		return nil
	}
	return m.lifecycleCall("agent.interrupt", map[string]any{"agent_id": target.id}, "Interrupted "+target.label, "")
}

func (m Model) lifecycleCall(method string, params map[string]any, notice, removedTerminal string) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var result map[string]any
		if err := client.Call(ctx, method, params, &result); err != nil {
			return lifecycleMsg{err: err}
		}
		return lifecycleMsg{notice: notice, removedTerminal: removedTerminal}
	}
}

// applyLifecycle records the outcome and drops any pane still attached to a
// session that has been removed.
func (m *Model) applyLifecycle(msg lifecycleMsg) tea.Cmd {
	m.pendingStop = ""
	m.err = msg.err
	if msg.err != nil {
		m.notice = ""
		return nil
	}
	m.notice = msg.notice
	if msg.removedTerminal != "" {
		if pane := m.findPane(msg.removedTerminal); pane != nil {
			m.removePane(pane)
		}
	}
	m.loading = true
	return m.loadSnapshot()
}
