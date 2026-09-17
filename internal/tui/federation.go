package tui

import (
	"context"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/martintrifunov/orkestar/internal/daemon"
)

// MachineView is another machine's board as last polled. Only agents are merged
// into the sidebar; a machine's workspaces, tasks and panes are shown when it
// is selected, because acting on them needs that machine's client.
type MachineView struct {
	ID       string
	Label    string
	State    string
	Err      string
	Snapshot daemon.Snapshot
}

// scopedAgent is an agent tagged with the machine it lives on, so a merged row
// can name its machine and route an action to it.
type scopedAgent struct {
	MachineID    string
	MachineLabel string
	Remote       bool
	Agent        daemon.Agent
}

// allAgents is the selected machine's agents followed by the agents polled from
// every other machine.
func (m Model) allAgents() []scopedAgent {
	current := m.currentMachine()
	agents := make([]scopedAgent, 0, len(m.snapshot.Agents))
	for _, agent := range m.snapshot.Agents {
		agents = append(agents, scopedAgent{MachineID: current.ID, MachineLabel: current.Label, Agent: agent})
	}
	for _, view := range m.remote {
		for _, agent := range view.Snapshot.Agents {
			agents = append(agents, scopedAgent{MachineID: view.ID, MachineLabel: view.Label, Remote: true, Agent: agent})
		}
	}
	return agents
}

// taskTitleOfScoped names the task a scoped agent works, reading the remote
// machine's own board when the agent is remote.
func (m Model) taskTitleOfScoped(scoped scopedAgent) string {
	if !scoped.Remote {
		return m.taskTitleOf(scoped.Agent.TaskID)
	}
	for _, view := range m.remote {
		if view.ID != scoped.MachineID {
			continue
		}
		for _, task := range view.Snapshot.Tasks {
			if task.ID == scoped.Agent.TaskID {
				return task.Title
			}
		}
	}
	return ""
}

// remotesMsg carries the polled boards of the machines that are not selected.
// machineIndex is the selection the poll was taken for, so a poll that lands
// after a switch can be dropped rather than showing the wrong set of machines.
type remotesMsg struct {
	views        []MachineView
	machineIndex int
}

// pollRemotes refreshes every other machine's board. The selected machine is
// polled by loadSnapshot; this is what keeps the merged agent list, and the
// machine status lines, current.
func (m Model) pollRemotes() tea.Cmd {
	if len(m.machines) < 2 {
		return nil
	}
	machines := m.machines
	active := m.machineIndex
	parent := m.ctx
	if parent == nil {
		parent = context.Background()
	}
	return func() tea.Msg {
		views := make([]MachineView, len(machines))
		var wg sync.WaitGroup
		for index, machine := range machines {
			if index == active {
				continue
			}
			wg.Add(1)
			go func(index int) {
				defer wg.Done()
				// One slow machine must not stall the others behind its
				// timeout, and quitting must not leave polls dialing: each
				// poll carries its own timeout under the interface context.
				view := MachineView{ID: machine.ID, Label: machine.Label, State: "online"}
				ctx, cancel := context.WithTimeout(parent, 3*time.Second)
				defer cancel()
				var snapshot daemon.Snapshot
				if err := machine.Client.Call(ctx, "system.snapshot", nil, &snapshot); err != nil {
					view.State = "offline"
					view.Err = err.Error()
				} else {
					view.Snapshot = snapshot
				}
				views[index] = view
			}(index)
		}
		wg.Wait()
		ordered := make([]MachineView, 0, len(machines)-1)
		for index, view := range views {
			if index == active {
				continue
			}
			ordered = append(ordered, view)
		}
		return remotesMsg{views: ordered, machineIndex: active}
	}
}
