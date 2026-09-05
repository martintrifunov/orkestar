package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/martintrifunov/orkestar/internal/daemon"
	"github.com/martintrifunov/orkestar/internal/workflow"
)

// settle runs a command and feeds each resulting message back, the way the
// program loop would, so a mutation and its snapshot reload both land.
func settle(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	for i := 0; cmd != nil && i < 6; i++ {
		msg := cmd()
		if msg == nil {
			return m
		}
		updated, next := m.Update(msg)
		m = updated.(Model)
		cmd = next
	}
	return m
}

func press(t *testing.T, m Model, code rune) (Model, tea.Cmd) {
	t.Helper()
	updated, cmd := m.Update(tea.KeyPressMsg{Code: code})
	return updated.(Model), cmd
}

func typeText(t *testing.T, m Model, text string) Model {
	t.Helper()
	for _, r := range text {
		m, _ = press(t, m, r)
	}
	return m
}

func taskRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, args := range [][]string{
		{"init"}, {"config", "user.name", "Fixture"}, {"config", "user.email", "fixture@example.invalid"},
	} {
		if b, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, b)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "file.txt"), []byte("base\n"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-m", "Initial"}} {
		if b, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, b)
		}
	}
	return root
}

// The sidebar could not create a task at all, so the panel was permanently
// empty for anyone who did not use the command line.
func TestTaskLifecycleFromTheSidebar(t *testing.T) {
	root := taskRepo(t)
	client := startEmbeddedTestDaemon(t)
	m := New(client, root)
	m.width, m.height = 160, 44
	m.focus = focusTasks

	m, cmd := press(t, m, 'c')
	if !m.taskPrompt || cmd != nil {
		t.Fatal("c did not open the new-task prompt")
	}
	if !m.taskReview {
		t.Fatal("auto-review should start on, matching the daemon default")
	}
	if view := m.promptView(); !strings.Contains(view, "New task") || !strings.Contains(view, "Auto-review: on") {
		t.Fatalf("prompt does not explain itself:\n%s", view)
	}
	// Tab opts out of the reviewer gate so this test can complete the task
	// without an adapter, and proves the toggle works.
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m = updated.(Model)
	if m.taskReview || !strings.Contains(m.promptView(), "Auto-review: off") {
		t.Fatal("tab did not toggle auto-review")
	}
	m = typeText(t, m, "Ship it")
	updated, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if m.taskPrompt || cmd == nil {
		t.Fatal("enter did not submit the task")
	}
	m = settle(t, m, cmd)
	if m.err != nil {
		t.Fatalf("create failed: %v", m.err)
	}
	if len(m.snapshot.Tasks) != 1 || m.snapshot.Tasks[0].Title != "Ship it" {
		t.Fatalf("task was not created: %+v", m.snapshot.Tasks)
	}
	if m.snapshot.Tasks[0].AutoReview {
		t.Fatal("auto-review opt-out was not sent")
	}
	if !strings.Contains(m.renderTasks(), "Ship it") {
		t.Fatal("the new task is not listed")
	}

	// A task with no worktree has nothing to diff, and says so instead of
	// surfacing the daemon's error.
	m, cmd = press(t, m, 'd')
	if cmd != nil || m.viewingDiff || !strings.Contains(m.notice, "no worktree") {
		t.Fatalf("diff without a worktree: cmd=%v notice=%q", cmd != nil, m.notice)
	}
	if !strings.Contains(m.renderTasks(), "no worktree") {
		t.Fatal("the missing worktree is not shown in the list")
	}

	m, cmd = press(t, m, 'w')
	if cmd == nil || !m.taskBusy {
		t.Fatal("w did not start creating a worktree")
	}
	m = settle(t, m, cmd)
	if m.err != nil || m.taskBusy {
		t.Fatalf("worktree creation failed: %v", m.err)
	}
	task := m.snapshot.Tasks[0]
	if task.WorktreePath == "" || task.WorktreeBranch == "" {
		t.Fatalf("no worktree recorded: %+v", task)
	}
	t.Cleanup(func() { os.RemoveAll(filepath.Dir(task.WorktreePath)) })
	if !strings.Contains(m.renderTasks(), "branch "+task.WorktreeBranch) {
		t.Fatal("branch is not shown")
	}

	// Now the diff opens, and carries the task's identity.
	if err := os.WriteFile(filepath.Join(task.WorktreePath, "file.txt"), []byte("changed\n"), 0644); err != nil {
		t.Fatal(err)
	}
	m, cmd = press(t, m, 'd')
	if cmd == nil {
		t.Fatal("d did not request the task diff")
	}
	m = settle(t, m, cmd)
	if !m.viewingDiff || m.diffErr != nil {
		t.Fatalf("task diff did not open: %v", m.diffErr)
	}
	if out := m.renderDiff(100); !strings.Contains(out, "Ship it") || !strings.Contains(out, "file.txt") {
		t.Fatalf("diff does not identify the task or its changes:\n%s", out)
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(Model)

	m, cmd = press(t, m, 'm')
	if cmd == nil || !m.taskBusy || m.notice != "Marking done…" {
		t.Fatalf("m did not report progress: busy=%v notice=%q", m.taskBusy, m.notice)
	}
	m = settle(t, m, cmd)
	if m.err != nil || m.snapshot.Tasks[0].Status != workflow.StatusDone {
		t.Fatalf("task was not completed: %v %+v", m.err, m.snapshot.Tasks[0])
	}
	if m.taskBusy || m.notice != "Task done" {
		t.Fatalf("completion was not reported: busy=%v notice=%q", m.taskBusy, m.notice)
	}

	// A second task can be abandoned rather than completed.
	m, _ = press(t, m, 'c')
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m = updated.(Model)
	m = typeText(t, m, "Drop it")
	updated, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = settle(t, updated.(Model), cmd)
	m.taskSelected = len(m.snapshot.Tasks) - 1
	if m.snapshot.Tasks[m.taskSelected].Title != "Drop it" {
		t.Fatalf("second task missing: %+v", m.snapshot.Tasks)
	}
	m, cmd = press(t, m, 'x')
	m = settle(t, m, cmd)
	if m.err != nil || m.snapshot.Tasks[m.taskSelected].Status != workflow.StatusCancelled {
		t.Fatalf("task was not cancelled: %v %+v", m.err, m.snapshot.Tasks[m.taskSelected])
	}
	if m.notice != "Task cancelled" {
		t.Fatalf("cancel was not reported: %q", m.notice)
	}
}

func TestCompletingAReviewedTaskSaysAReviewerIsRunning(t *testing.T) {
	m := Model{width: 140, height: 40, focus: focusTasks}
	m.snapshot.Tasks = []workflow.Task{{ID: "task_1", Title: "Reviewed", AutoReview: true, Status: workflow.StatusPending}}
	m, cmd := press(t, m, 'm')
	if cmd == nil || !m.taskBusy {
		t.Fatal("m did not start")
	}
	if !strings.Contains(m.notice, "reviewer agent is running") {
		t.Fatalf("the reviewer run is invisible: %q", m.notice)
	}
	// A second press cannot stack another reviewer run on top.
	busy, second := press(t, m, 'm')
	if second != nil || !busy.taskBusy {
		t.Fatal("a second completion was accepted while one was in flight")
	}
	// A rejection is reported and clears the busy state.
	updated, _ := m.Update(taskActionMsg{err: errReviewRejected})
	m = updated.(Model)
	if m.taskBusy || m.err == nil {
		t.Fatalf("rejection left the UI stuck: busy=%v err=%v", m.taskBusy, m.err)
	}
}

var errReviewRejected = &taskError{"reviewer requested changes: missing tests"}

type taskError struct{ text string }

func (e *taskError) Error() string { return e.text }

func TestTaskDetailShowsWhatChangesBehaviour(t *testing.T) {
	m := Model{width: 140, height: 40, focus: focusTasks}
	m.snapshot.Tasks = []workflow.Task{
		{ID: "task_1", Title: "First", Status: workflow.StatusInProgress},
		{ID: "task_2", Title: "Second", Status: workflow.StatusPending, DependsOn: []string{"task_1"},
			AutoReview: true, AssigneeAgentID: "agent_9", WorktreePath: "/tmp/w", WorktreeBranch: "task/2"},
	}
	m.taskSelected = 1
	out := m.renderTasks()
	for _, want := range []string{"blocked by 1", "review before done", "agent agent_9", "branch task/2"} {
		if !strings.Contains(out, want) {
			t.Errorf("task detail is missing %q:\n%s", want, out)
		}
	}
	// Detail belongs to the selected task only, so the list stays readable.
	m.taskSelected = 0
	if strings.Contains(m.renderTasks(), "review before done") {
		t.Fatal("detail leaked onto an unselected task")
	}
	// A finished dependency no longer blocks.
	m.snapshot.Tasks[0].Status = workflow.StatusDone
	m.taskSelected = 1
	if strings.Contains(m.renderTasks(), "blocked by") {
		t.Fatal("a completed dependency still reports as blocking")
	}
}

func TestCancelAndAssignShareKeysWithoutColliding(t *testing.T) {
	m := Model{width: 140, height: 40, focus: focusAgents}
	m.snapshot.Tasks = []workflow.Task{{ID: "task_1", Title: "One", Status: workflow.StatusPending}}
	m.snapshot.Agents = []daemon.Agent{{ID: "agent_1", Adapter: "claude-code"}}
	m.snapshot.Permissions = []daemon.PermissionRequest{{ID: "perm_1", AgentID: "agent_1"}}

	// With Agents focused, x still answers the permission request.
	if _, cmd := press(t, m, 'x'); cmd == nil {
		t.Fatal("x no longer denies a permission request")
	}
	// With Tasks focused, x cancels the task instead.
	m.focus = focusTasks
	m, cmd := press(t, m, 'x')
	if cmd == nil || !m.taskBusy {
		t.Fatal("x did not cancel the selected task")
	}

	// Assign targets the agent highlighted in the Agents list.
	m.taskBusy = false
	m, cmd = press(t, m, 't')
	if cmd == nil {
		t.Fatal("t did not assign the task")
	}
	m.snapshot.Agents = nil
	m.agentSelected = 0
	m, cmd = press(t, m, 't')
	if cmd != nil || !strings.Contains(m.notice, "No agent to assign") {
		t.Fatalf("assigning without an agent: cmd=%v notice=%q", cmd != nil, m.notice)
	}
}

func TestTaskPromptCancelsAndIgnoresAnEmptyTitle(t *testing.T) {
	m := Model{width: 140, height: 40, focus: focusTasks}
	m, _ = press(t, m, 'c')
	m = typeText(t, m, "draft")
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(Model)
	if m.taskPrompt {
		t.Fatal("esc did not close the prompt")
	}
	m, _ = press(t, m, 'c')
	if m.taskTitle != "" {
		t.Fatal("the cancelled draft came back")
	}
	updated, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if cmd != nil || m.taskPrompt {
		t.Fatal("an empty title created a task")
	}
	// The prompt owns the keyboard while it is open.
	m, _ = press(t, m, 'c')
	m = typeText(t, m, "q")
	if !m.taskPrompt || m.taskTitle != "q" {
		t.Fatal("quit key was not captured by the prompt")
	}
}
