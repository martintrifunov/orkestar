package tui

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/martintrifunov/orkestar/internal/ipc"
)

// lifecycleMsg reports a stop, removal or interrupt. removedTerminal lets the
// model close a pane that was attached to a session that no longer exists.
type lifecycleMsg struct {
	notice          string
	removedTerminal string
	err             error
	// machineID is the machine the call ran against, so a reply that
	// lands after a switch is not applied to the new machine's board.
	machineID string
	// foreign marks an action taken on another machine's agent row; its
	// result refreshes that machine's polled view instead of the local board.
	foreign bool
}

// selectedLifecycleTarget describes what the sidebar is pointing at, so one
// key can act on either a session or an agent. client and machineID say where
// the call goes: an agent row from another machine is driven on its own
// daemon.
type lifecycleTarget struct {
	kind, id, terminalID, label, state string
	machineID                          string
	client                             *ipc.Client
	foreign                            bool
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
			return lifecycleTarget{
				kind: "terminal", id: t.ID, terminalID: t.ID, label: label, state: t.State,
				machineID: m.currentMachine().ID, client: m.client,
			}, true
		}
	case focusAgents:
		scoped, ok := m.scopedAgentAt(m.agentSelected)
		if !ok {
			return lifecycleTarget{}, false
		}
		machine, ok := m.agentMachine(scoped)
		if !ok {
			return lifecycleTarget{}, false
		}
		a := scoped.Agent
		return lifecycleTarget{
			kind: "agent", id: a.ID, terminalID: a.TerminalID, label: a.Adapter, state: a.State,
			machineID: machine.ID, client: machine.Client, foreign: scoped.Remote,
		}, true
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
		return m.lifecycleCall(target, method, params, "Stopped "+target.label, "")
	}
	m.pendingStop = ""
	method := "terminal.remove"
	params := map[string]any{"terminal_id": target.id}
	if target.kind == "agent" {
		method, params = "agent.remove", map[string]any{"agent_id": target.id}
	}
	removedTerminal := target.terminalID
	if target.foreign {
		// A foreign pane is on that machine's own layout, not this client's.
		removedTerminal = ""
	}
	return m.lifecycleCall(target, method, params, "Removed "+target.label, removedTerminal)
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
	return m.lifecycleCall(target, "agent.interrupt", map[string]any{"agent_id": target.id}, "Interrupted "+target.label, "")
}

func (m Model) lifecycleCall(target lifecycleTarget, method string, params map[string]any, notice, removedTerminal string) tea.Cmd {
	client, machineID, foreign := target.client, target.machineID, target.foreign
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var result map[string]any
		if err := client.Call(ctx, method, params, &result); err != nil {
			return lifecycleMsg{err: err, machineID: machineID, foreign: foreign}
		}
		return lifecycleMsg{notice: notice, removedTerminal: removedTerminal, machineID: machineID, foreign: foreign}
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
