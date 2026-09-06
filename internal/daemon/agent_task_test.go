package daemon_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/martintrifunov/orkestar/internal/daemon"
	"github.com/martintrifunov/orkestar/internal/ipc"
	"github.com/martintrifunov/orkestar/internal/workflow"
)

// taskAgentFixture is a running daemon with one workspace over a real git
// repository, so a task can be given a worktree.
type taskAgentFixture struct {
	client    *ipc.Client
	adapter   *controllableAdapter
	workspace daemon.Workspace
	directory string
}

func newTaskAgentFixture(t *testing.T) taskAgentFixture {
	t.Helper()

	directory := initRepo(t)
	socketDirectory, err := os.MkdirTemp("/tmp", "orkestar-agent-task-")
	if err != nil {
		t.Fatalf("create socket directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDirectory) })

	server := daemon.NewServer(filepath.Join(socketDirectory, "orkestar.sock"))
	adapter := newControllableAdapter("fake-agent")
	server.RegisterAdapter(adapter)

	ctx, cancel := context.WithCancel(context.Background())
	serverError := make(chan error, 1)
	go func() { serverError <- server.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-serverError; err != nil {
			t.Errorf("server shutdown: %v", err)
		}
	})

	client := ipc.NewClient(filepath.Join(socketDirectory, "orkestar.sock"))
	waitForServer(t, client)

	callContext, callCancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(callCancel)

	var workspace daemon.Workspace
	if err := client.Call(callContext, "workspace.create", map[string]string{"directory": directory}, &workspace); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	return taskAgentFixture{client: client, adapter: adapter, workspace: workspace, directory: directory}
}

func (f taskAgentFixture) call(t *testing.T, method string, params any, result any) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return f.client.Call(ctx, method, params, result)
}

func (f taskAgentFixture) mustCall(t *testing.T, method string, params any, result any) {
	t.Helper()
	if err := f.call(t, method, params, result); err != nil {
		t.Fatalf("%s: %v", method, err)
	}
}

func (f taskAgentFixture) createTask(t *testing.T, title string) workflow.Task {
	t.Helper()
	var task workflow.Task
	f.mustCall(t, "task.create", map[string]any{
		"workspace_id": f.workspace.ID, "title": title, "auto_review": false,
	}, &task)
	return task
}

func (f taskAgentFixture) task(t *testing.T, id string) workflow.Task {
	t.Helper()
	var snapshot daemon.Snapshot
	f.mustCall(t, "system.snapshot", nil, &snapshot)
	for _, task := range snapshot.Tasks {
		if task.ID == id {
			return task
		}
	}
	t.Fatalf("task %q is not in the snapshot", id)
	return workflow.Task{}
}

// Launching an agent for a task is what ties the two together: the session
// records the task and the board records the agent, in one call, so neither
// can be left pointing at nothing.
func TestLaunchForTaskAssignsBothWays(t *testing.T) {
	t.Parallel()

	fixture := newTaskAgentFixture(t)
	task := fixture.createTask(t, "wire the thing up")

	var launched daemon.Agent
	fixture.mustCall(t, "agent.launch", map[string]any{
		"workspace_id": fixture.workspace.ID, "adapter": "fake-agent", "task_id": task.ID,
	}, &launched)

	if launched.TaskID != task.ID {
		t.Fatalf("agent records task %q, want %q", launched.TaskID, task.ID)
	}
	if assigned := fixture.task(t, task.ID); assigned.AssigneeAgentID != launched.ID {
		t.Fatalf("task is assigned to %q, want %q", assigned.AssigneeAgentID, launched.ID)
	}
}

// An agent working a task with a worktree belongs in that worktree, or its
// changes land in the workspace's own checkout and the task diff shows
// nothing.
func TestLaunchForTaskUsesItsWorktree(t *testing.T) {
	t.Parallel()

	fixture := newTaskAgentFixture(t)
	task := fixture.createTask(t, "work in its own checkout")

	var withWorktree workflow.Task
	fixture.mustCall(t, "task.createWorktree", map[string]any{"task_id": task.ID}, &withWorktree)
	if withWorktree.WorktreePath == "" {
		t.Fatal("task has no worktree")
	}

	var launched daemon.Agent
	fixture.mustCall(t, "agent.launch", map[string]any{
		"workspace_id": fixture.workspace.ID, "adapter": "fake-agent", "task_id": task.ID,
	}, &launched)

	session := fixture.adapter.launched(t)
	if session.options.Directory != withWorktree.WorktreePath {
		t.Fatalf("agent launched in %q, want the task worktree %q", session.options.Directory, withWorktree.WorktreePath)
	}
}

// A task with no worktree still launches, in the workspace directory, because
// plenty of work does not need its own branch.
func TestLaunchForTaskWithoutWorktreeUsesTheWorkspace(t *testing.T) {
	t.Parallel()

	fixture := newTaskAgentFixture(t)
	task := fixture.createTask(t, "no worktree needed")

	var launched daemon.Agent
	fixture.mustCall(t, "agent.launch", map[string]any{
		"workspace_id": fixture.workspace.ID, "adapter": "fake-agent", "task_id": task.ID,
	}, &launched)

	if session := fixture.adapter.launched(t); session.options.Directory != fixture.directory {
		t.Fatalf("agent launched in %q, want the workspace %q", session.options.Directory, fixture.directory)
	}
}

func TestLaunchRejectsAnUnknownTask(t *testing.T) {
	t.Parallel()

	fixture := newTaskAgentFixture(t)
	var launched daemon.Agent
	err := fixture.call(t, "agent.launch", map[string]any{
		"workspace_id": fixture.workspace.ID, "adapter": "fake-agent", "task_id": "task_missing",
	}, &launched)
	if err == nil {
		t.Fatal("launching for a task that does not exist should fail")
	}
}

// The point of the association: the board follows what the agent actually
// does, instead of waiting for someone to remember to move the task.
func TestFirstPromptStartsTheTask(t *testing.T) {
	t.Parallel()

	fixture := newTaskAgentFixture(t)
	task := fixture.createTask(t, "starts on its own")

	var launched daemon.Agent
	fixture.mustCall(t, "agent.launch", map[string]any{
		"workspace_id": fixture.workspace.ID, "adapter": "fake-agent", "task_id": task.ID,
	}, &launched)

	if before := fixture.task(t, task.ID); before.Status != workflow.StatusPending {
		t.Fatalf("task is %q before any prompt, want pending", before.Status)
	}

	fixture.hook(t, fixture.adapter.launched(t), launched.ID, "UserPromptSubmit")

	if after := fixture.task(t, task.ID); after.Status != workflow.StatusInProgress {
		t.Fatalf("task is %q after a prompt, want in_progress", after.Status)
	}
}

// A prompt says work is happening, not that it is finished or abandoned, so it
// must not drag a task backwards out of a status a human chose.
func TestPromptsDoNotOverruleADecidedStatus(t *testing.T) {
	t.Parallel()

	for _, status := range []workflow.Status{workflow.StatusDone, workflow.StatusCancelled} {
		t.Run(string(status), func(t *testing.T) {
			fixture := newTaskAgentFixture(t)
			task := fixture.createTask(t, "already decided")

			var launched daemon.Agent
			fixture.mustCall(t, "agent.launch", map[string]any{
				"workspace_id": fixture.workspace.ID, "adapter": "fake-agent", "task_id": task.ID,
			}, &launched)

			var updated workflow.Task
			fixture.mustCall(t, "task.setStatus", map[string]any{"task_id": task.ID, "status": string(status)}, &updated)

			fixture.hook(t, fixture.adapter.launched(t), launched.ID, "UserPromptSubmit")

			if after := fixture.task(t, task.ID); after.Status != status {
				t.Fatalf("task is %q after a prompt, want it left at %q", after.Status, status)
			}
		})
	}
}

// An agent launched without a task must not touch the board at all.
func TestPromptWithoutATaskChangesNothing(t *testing.T) {
	t.Parallel()

	fixture := newTaskAgentFixture(t)
	task := fixture.createTask(t, "nobody is working on this")

	var launched daemon.Agent
	fixture.mustCall(t, "agent.launch", map[string]any{
		"workspace_id": fixture.workspace.ID, "adapter": "fake-agent",
	}, &launched)

	fixture.hook(t, fixture.adapter.launched(t), launched.ID, "UserPromptSubmit")

	if after := fixture.task(t, task.ID); after.Status != workflow.StatusPending {
		t.Fatalf("unrelated task moved to %q", after.Status)
	}
}

// A blocked task cannot start, and the hook must not fail because of it: the
// agent is waiting on that reply to carry on.
func TestABlockedTaskStaysPendingAndTheHookSucceeds(t *testing.T) {
	t.Parallel()

	fixture := newTaskAgentFixture(t)
	blocker := fixture.createTask(t, "must happen first")

	var dependent workflow.Task
	fixture.mustCall(t, "task.create", map[string]any{
		"workspace_id": fixture.workspace.ID, "title": "waits its turn",
		"depends_on": []string{blocker.ID}, "auto_review": false,
	}, &dependent)

	var launched daemon.Agent
	fixture.mustCall(t, "agent.launch", map[string]any{
		"workspace_id": fixture.workspace.ID, "adapter": "fake-agent", "task_id": dependent.ID,
	}, &launched)

	fixture.hook(t, fixture.adapter.launched(t), launched.ID, "UserPromptSubmit")

	if after := fixture.task(t, dependent.ID); after.Status != workflow.StatusPending {
		t.Fatalf("blocked task moved to %q, want pending", after.Status)
	}
}

// hook delivers a lifecycle hook the way an agent's own bridge would, with
// the token the daemon handed that session.
func (f taskAgentFixture) hook(t *testing.T, session *controllableSession, agentID, event string) {
	t.Helper()
	var result map[string]string
	if err := f.call(t, "agent.hook", daemon.HookInput{
		AgentID: agentID, Token: session.hookToken(), Event: event,
	}, &result); err != nil {
		t.Fatalf("hook %s: %v", event, err)
	}
}

// Editing over IPC has to preserve the fields it was not given: JSON makes an
// absent key and an empty string easy to confuse, and confusing them here
// silently destroys a description.
func TestTaskUpdateLeavesAbsentFieldsAlone(t *testing.T) {
	t.Parallel()

	fixture := newTaskAgentFixture(t)
	var created workflow.Task
	fixture.mustCall(t, "task.create", map[string]any{
		"workspace_id": fixture.workspace.ID, "title": "First",
		"description": "as written", "auto_review": false,
	}, &created)

	var renamed workflow.Task
	fixture.mustCall(t, "task.update", map[string]any{"task_id": created.ID, "title": "Renamed"}, &renamed)
	if renamed.Title != "Renamed" || renamed.Description != "as written" {
		t.Fatalf("unexpected task: %+v", renamed)
	}

	var cleared workflow.Task
	fixture.mustCall(t, "task.update", map[string]any{"task_id": created.ID, "description": ""}, &cleared)
	if cleared.Description != "" {
		t.Fatalf("an explicit empty description was ignored: %q", cleared.Description)
	}
	if cleared.Title != "Renamed" {
		t.Fatalf("the title was lost: %q", cleared.Title)
	}
}

// A dependency added after the fact can close a loop that Create could never
// build, and the daemon must refuse it rather than store an unstartable board.
func TestTaskUpdateRefusesACycleOverIPC(t *testing.T) {
	t.Parallel()

	fixture := newTaskAgentFixture(t)
	first := fixture.createTask(t, "First")
	var second workflow.Task
	fixture.mustCall(t, "task.create", map[string]any{
		"workspace_id": fixture.workspace.ID, "title": "Second",
		"depends_on": []string{first.ID}, "auto_review": false,
	}, &second)

	var updated workflow.Task
	err := fixture.call(t, "task.update", map[string]any{
		"task_id": first.ID, "depends_on": []string{second.ID},
	}, &updated)
	if err == nil {
		t.Fatal("a cycle was accepted")
	}
	if after := fixture.task(t, first.ID); len(after.DependsOn) != 0 {
		t.Fatalf("the rejected edit was stored anyway: %v", after.DependsOn)
	}
}
