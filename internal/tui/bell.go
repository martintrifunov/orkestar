package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/martintrifunov/orkestar/internal/daemon"
	"github.com/martintrifunov/orkestar/internal/workflow"
)

// bell is BEL. Whether it makes a sound, flashes, or posts a notification is
// the terminal's business, which is the point: it is the one signal every
// terminal already knows how to deliver to a user who is looking elsewhere.
const bell = "\a"

// finishedAgentStates are the states an agent does not come back from.
func finishedAgentState(state string) bool {
	return state == "stopped" || state == "crashed" || state == "interrupted"
}

// bellFor reports what is worth interrupting the user for between two
// snapshots, and the notice to show alongside it.
//
// Only two things qualify, and both share a shape: work the user handed off
// has reached a point where it needs them again. A task that finished is the
// good case. An agent that stopped while its task was still open is the bad
// one, and it is the one worth catching, because nothing else announces it:
// the session simply stops producing output.
func bellFor(previous, current daemon.Snapshot) string {
	before := make(map[string]workflow.Task, len(previous.Tasks))
	for _, task := range previous.Tasks {
		before[task.ID] = task
	}
	for _, task := range current.Tasks {
		if was, ok := before[task.ID]; ok && was.Status != workflow.StatusDone && task.Status == workflow.StatusDone {
			return "Task done: " + task.Title
		}
	}

	tasks := make(map[string]workflow.Task, len(current.Tasks))
	for _, task := range current.Tasks {
		tasks[task.ID] = task
	}
	agents := make(map[string]daemon.Agent, len(previous.Agents))
	for _, agent := range previous.Agents {
		agents[agent.ID] = agent
	}
	for _, agent := range current.Agents {
		was, ok := agents[agent.ID]
		if !ok || finishedAgentState(was.State) || !finishedAgentState(agent.State) {
			continue
		}
		// An agent with no task stopping is ordinary: the user closed it.
		// One that was working a task nobody has finished is not.
		task, ok := tasks[agent.TaskID]
		if !ok || task.Status == workflow.StatusDone || task.Status == workflow.StatusCancelled {
			continue
		}
		return agent.Adapter + " stopped with " + task.Title + " unfinished"
	}
	return ""
}

// ring returns the command that sounds the bell, or nil when the user has
// turned it off. tea.Raw writes straight to the terminal, so the byte reaches
// it without going through a frame.
func (m Model) ring() tea.Cmd {
	if !m.settings.bellEnabled() {
		return nil
	}
	return tea.Raw(bell)
}
