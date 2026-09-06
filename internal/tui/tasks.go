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

// taskActionMsg reports the result of any task mutation. Every successful
// change reloads the snapshot, so the sidebar shows daemon state rather than a
// locally guessed one.
type taskActionMsg struct {
	task   workflow.Task
	notice string
	err    error
}

type diffMsg struct {
	diff   daemon.TaskDiff
	err    error
	taskID string
}

// selectedTask returns the highlighted task, if the sidebar has one.
func (m Model) selectedTask() (workflow.Task, bool) {
	if m.focus != focusTasks || m.taskSelected >= len(m.snapshot.Tasks) {
		return workflow.Task{}, false
	}
	return m.snapshot.Tasks[m.taskSelected], true
}

// blockedBy counts the task's dependencies that have not finished. A task
// cannot start while any remain.
func (m Model) blockedBy(task workflow.Task) int {
	blocked := 0
	for _, id := range task.DependsOn {
		for _, other := range m.snapshot.Tasks {
			if other.ID == id && other.Status != workflow.StatusDone {
				blocked++
			}
		}
	}
	return blocked
}

func (m Model) renderTasks() string {
	lines := []string{accentStyle.Render("Tasks")}
	if len(m.snapshot.Tasks) == 0 {
		return strings.Join(append(lines, dimStyle.Render("No tasks yet."), dimStyle.Render("Press c to create one.")), "\n")
	}
	for index, task := range m.snapshot.Tasks {
		line := fmt.Sprintf("%-11s  %s", task.Status, task.Title)
		selected := m.focus == focusTasks && index == m.taskSelected
		if selected {
			line = selectedStyle.Render(" " + line + " ")
		} else {
			line = "  " + line
		}
		lines = append(lines, line)
		if task.WorktreePath != "" {
			lines = append(lines, dimStyle.Render("    branch "+task.WorktreeBranch))
		}
		// The detail lines are only worth their rows for the task being acted
		// on. The description is why the task exists, so it comes first.
		// An open pane is marked on every row, not just the selected one: it is
		// how the list answers "which of this is on screen".
		if group := m.panesForTask(task.ID); len(group) > 0 {
			mark := "    on screen"
			if len(group) > 1 {
				mark = fmt.Sprintf("    on screen · %d panes", len(group))
			}
			lines = append(lines, accentStyle.Render(mark))
		}
		if selected {
			if task.Description != "" {
				lines = append(lines, dimStyle.Render("    "+task.Description))
			}
			if detail := m.taskDetail(task); detail != "" {
				lines = append(lines, dimStyle.Render("    "+detail))
			}
		}
	}
	return strings.Join(lines, "\n")
}

// taskDetail summarizes what the one-line entry cannot show but that changes
// what the task will do: whether it is blocked, whether completing it runs a
// reviewer, who owns it, and whether it has somewhere to make changes.
func (m Model) taskDetail(task workflow.Task) string {
	var parts []string
	if blocked := m.blockedBy(task); blocked > 0 {
		parts = append(parts, fmt.Sprintf("blocked by %d", blocked))
	}
	if task.AutoReview {
		parts = append(parts, "review before done")
	}
	if adapter := m.agentAdapterOf(task.AssigneeAgentID); adapter != "" {
		parts = append(parts, adapter+" "+m.agentStateOf(task.AssigneeAgentID))
	} else if task.AssigneeAgentID != "" {
		parts = append(parts, "agent "+task.AssigneeAgentID)
	} else {
		parts = append(parts, "unstarted (a)")
	}
	if task.WorktreePath == "" {
		parts = append(parts, "no worktree (w)")
	}
	return strings.Join(parts, " · ")
}

// taskCall runs one task mutation and reports it back with a notice to show
// on success.
func (m Model) taskCall(method string, params map[string]any, notice string, timeout time.Duration) tea.Cmd {
	client := m.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		var task workflow.Task
		err := client.Call(ctx, method, params, &task)
		return taskActionMsg{task: task, notice: notice, err: err}
	}
}

// startTaskPrompt opens the new-task prompt. Auto-review starts on, matching
// the daemon and CLI default, and the prompt says what that means.
func (m *Model) startTaskPrompt() {
	m.taskPrompt = true
	m.taskTitle, m.taskDescription, m.taskEditID, m.taskField = "", "", "", 0
	m.taskReview = true
	m.focus = focusTasks
}

// startTaskEdit opens the same prompt over an existing task. What a task is
// for changes as it is worked on, and until now the only way to correct a
// title was to cancel the task and make another one.
func (m *Model) startTaskEdit() {
	task, ok := m.selectedTask()
	if !ok {
		return
	}
	m.taskPrompt = true
	m.taskTitle, m.taskDescription = task.Title, task.Description
	m.taskEditID, m.taskField = task.ID, 0
	m.taskReview = task.AutoReview
}

// editTask saves the prompt over the task it was opened on. Auto-review is not
// sent: it is a property of how the task was created, and changing it here
// would silently drop a review gate someone asked for.
func (m Model) editTask(taskID, title, description string) tea.Cmd {
	return m.taskCall("task.update", map[string]any{
		"task_id": taskID, "title": title, "description": description,
	}, "Task updated", 15*time.Second)
}

func (m Model) createTask(title, description string, autoReview bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		workspaceID, err := m.ensureWorkspace(ctx)
		if err != nil {
			return taskActionMsg{err: err}
		}
		var task workflow.Task
		err = m.client.Call(ctx, "task.create", map[string]any{
			"workspace_id": workspaceID,
			"title":        title,
			"description":  description,
			"auto_review":  autoReview,
		}, &task)
		return taskActionMsg{task: task, notice: "Task created", err: err}
	}
}

// markSelectedTaskDone completes a task. When the task has auto-review the
// daemon runs a reviewer agent first and only then accepts the change, which
// can take a while, so the caller shows that this is happening.
func (m Model) markSelectedTaskDone() tea.Cmd {
	task, ok := m.selectedTask()
	if !ok {
		return nil
	}
	return m.taskCall("task.setStatus", map[string]any{
		"task_id": task.ID, "status": string(workflow.StatusDone),
	}, "Task done", 5*time.Minute)
}

func (m Model) cancelSelectedTask() tea.Cmd {
	task, ok := m.selectedTask()
	if !ok {
		return nil
	}
	return m.taskCall("task.setStatus", map[string]any{
		"task_id": task.ID, "status": string(workflow.StatusCancelled),
	}, "Task cancelled", 15*time.Second)
}

// toggleSelectedWorktree gives a task its own checkout, or removes it. An
// agent needs one to make changes without disturbing the workspace, and the
// task diff reads from it.
func (m *Model) toggleSelectedWorktree() tea.Cmd {
	task, ok := m.selectedTask()
	if !ok {
		return nil
	}
	if task.WorktreePath == "" {
		return m.taskCall("task.createWorktree", map[string]any{"task_id": task.ID}, "Worktree created", 60*time.Second)
	}
	return m.taskCall("task.removeWorktree", map[string]any{"task_id": task.ID}, "Worktree removed", 60*time.Second)
}

// assignSelectedTask hands the task to the agent highlighted in the Agents
// list, which is visible in the sidebar while choosing.
func (m *Model) assignSelectedTask() tea.Cmd {
	task, ok := m.selectedTask()
	if !ok {
		return nil
	}
	if m.agentSelected >= len(m.snapshot.Agents) {
		m.notice = "No agent to assign. Launch one with a, then select it in Agents."
		return nil
	}
	agent := m.snapshot.Agents[m.agentSelected]
	return m.taskCall("task.assign", map[string]any{
		"task_id": task.ID, "agent_id": agent.ID,
	}, "Assigned to "+agent.Adapter, 15*time.Second)
}

// loadDiff opens the task's changed files, its diff and the latest reviewer
// verdict. The daemon reads the task's own worktree, so a task without one is
// reported as a missing step rather than as a failure.
func (m *Model) loadDiff() tea.Cmd {
	task, ok := m.selectedTask()
	if !ok {
		return nil
	}
	if task.WorktreePath == "" {
		m.notice = "This task has no worktree yet. Press w to create one."
		return nil
	}
	client := m.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		var diff daemon.TaskDiff
		err := client.Call(ctx, "task.diff", map[string]string{"task_id": task.ID}, &diff)
		return diffMsg{diff: diff, err: err, taskID: task.ID}
	}
}

// cursorOn marks the field typing goes to, so two editable lines can be told
// apart at a glance.
func (m Model) cursorOn(field int, value string) string {
	if m.taskField == field {
		return value + "▏"
	}
	return value
}

func (m Model) taskPromptView() string {
	review := "on — a reviewer agent must approve before this task can be done"
	if !m.taskReview {
		review = "off — the task can be completed without a review"
	}
	description := m.cursorOn(1, m.taskDescription)
	if description == "" {
		description = dimStyle.Render("what done looks like, or why this exists")
	}
	body := "New task\n\nWorkspace: " + m.directory +
		"\n\nTitle: " + m.cursorOn(0, m.taskTitle) +
		"\nDescription: " + description +
		"\n\nAuto-review: " + review +
		"\n\nEnter creates · Tab switches field · Ctrl+R toggles auto-review · Esc cancels"
	if m.taskEditID != "" {
		// Auto-review is left out: editing does not change it.
		body = "Edit task\n\nTitle: " + m.cursorOn(0, m.taskTitle) +
			"\nDescription: " + description +
			"\n\nEnter saves · Tab switches field · Esc cancels"
	}
	return body
}

func (m Model) updateTaskPrompt(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// Ctrl+R rather than Tab, which now moves between the two fields.
	if k.Mod&tea.ModCtrl != 0 && k.Code == 'r' && m.taskEditID == "" {
		m.taskReview = !m.taskReview
		return m, nil
	}
	switch k.Code {
	case tea.KeyEscape:
		m.taskPrompt = false
		return m, nil
	case tea.KeyTab:
		m.taskField = (m.taskField + 1) % 2
		return m, nil
	case tea.KeyEnter:
		title := strings.TrimSpace(m.taskTitle)
		description := strings.TrimSpace(m.taskDescription)
		if title == "" {
			// Keep the prompt open. Closing it would throw away a description
			// the user may have just written, with nothing said about why.
			m.taskField = 0
			m.notice = "A task needs a title."
			return m, nil
		}
		m.taskPrompt = false
		m.taskBusy = true
		if m.taskEditID != "" {
			m.notice = "Saving task…"
			return m, m.editTask(m.taskEditID, title, description)
		}
		m.notice = "Creating task…"
		return m, m.createTask(title, description, m.taskReview)
	case tea.KeyBackspace:
		field := m.field()
		if r := []rune(*field); len(r) > 0 {
			*field = string(r[:len(r)-1])
		}
		return m, nil
	}
	field := m.field()
	if k.Text != "" && k.Mod&(tea.ModCtrl|tea.ModAlt) == 0 {
		*field += k.Text
	} else if k.Mod == 0 && k.Code >= 32 && k.Code < 127 {
		*field += string(k.Code)
	}
	return m, nil
}

// field is the prompt line typing currently goes to.
func (m *Model) field() *string {
	if m.taskField == 1 {
		return &m.taskDescription
	}
	return &m.taskTitle
}

// pickerTask is the task the agent picker was opened for, if it was opened
// from a task rather than from the general "launch an agent" key.
func (m Model) pickerTask() (workflow.Task, bool) {
	if m.pickerTaskID == "" {
		return workflow.Task{}, false
	}
	for _, task := range m.snapshot.Tasks {
		if task.ID == m.pickerTaskID {
			return task, true
		}
	}
	return workflow.Task{}, false
}

// taskTitleOf names a task for a line that is about something else, such as
// the agent working it.
func (m Model) taskTitleOf(taskID string) string {
	if taskID == "" {
		return ""
	}
	for _, task := range m.snapshot.Tasks {
		if task.ID == taskID {
			return task.Title
		}
	}
	return ""
}

// agentAdapterOf and agentStateOf describe a task's assignee in the terms the
// Agents list uses, so the same session reads the same way in both places. An
// assignee the daemon no longer knows about returns empty.
func (m Model) agentAdapterOf(agentID string) string {
	for _, agent := range m.snapshot.Agents {
		if agent.ID == agentID {
			return agent.Adapter
		}
	}
	return ""
}

func (m Model) agentStateOf(agentID string) string {
	for _, agent := range m.snapshot.Agents {
		if agent.ID == agentID {
			return agent.State
		}
	}
	return ""
}
