package daemon_test

import (
	"strings"
	"testing"
	"time"

	"github.com/martintrifunov/orkestar/internal/daemon"
	"github.com/martintrifunov/orkestar/internal/workflow"
)

const autoStartChain = `{
  "name": "chain",
  "tasks": [
    {"key": "first", "title": "First", "agent": "fake-agent", "auto_start": true, "auto_review": false},
    {"key": "second", "title": "Second", "agent": "fake-agent", "auto_start": true, "auto_review": false, "depends_on": ["first"]}
  ]
}`

// waitForAutoStart blocks until the daemon has either launched the task or
// recorded why it could not, which are the two ways an automatic start ends.
func (f taskAgentFixture) waitForAutoStart(t *testing.T, taskID string) workflow.Task {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		task := f.task(t, taskID)
		if task.AssigneeAgentID != "" || task.AutoStartError != "" {
			return task
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("task %q was never auto-started", taskID)
	return workflow.Task{}
}

func TestTemplateAutoStartRunsAChain(t *testing.T) {
	t.Parallel()

	fixture := newTaskAgentFixture(t)
	fixture.writeTemplate(t, "chain", autoStartChain)

	var applied daemon.AppliedTemplate
	fixture.mustCall(t, "template.apply", map[string]any{
		"workspace_id": fixture.workspace.ID, "name": "chain", "start": true,
	}, &applied)

	if len(applied.Agents) != 0 {
		t.Fatalf("auto-start tasks were launched by the apply call: %+v", applied.Agents)
	}
	if len(applied.AutoStarting) != 2 {
		t.Fatalf("expected both tasks to be handed to the watcher, got %v", applied.AutoStarting)
	}
	if len(applied.Waiting) != 0 {
		t.Fatalf("auto-start tasks were also reported waiting: %v", applied.Waiting)
	}

	first := fixture.waitForAutoStart(t, applied.Tasks[0].ID)
	if first.AssigneeAgentID == "" {
		t.Fatalf("the dependency-free task was not started: %+v", first)
	}
	second := fixture.task(t, applied.Tasks[1].ID)
	if second.AssigneeAgentID != "" {
		t.Fatal("the dependent task started before its dependency finished")
	}

	var completed workflow.Task
	fixture.mustCall(t, "task.setStatus", map[string]any{
		"task_id": first.ID, "status": "done",
	}, &completed)

	second = fixture.waitForAutoStart(t, second.ID)
	if second.AssigneeAgentID == "" {
		t.Fatalf("the dependent task was not started after its dependency finished: %+v", second)
	}
}

func TestTemplateWithoutStartingDoesNotScheduleAutoStart(t *testing.T) {
	t.Parallel()

	fixture := newTaskAgentFixture(t)
	fixture.writeTemplate(t, "chain", autoStartChain)

	var applied daemon.AppliedTemplate
	fixture.mustCall(t, "template.apply", map[string]any{
		"workspace_id": fixture.workspace.ID, "name": "chain", "start": false,
	}, &applied)

	for _, task := range applied.Tasks {
		stored := fixture.task(t, task.ID)
		if stored.AutoStart {
			t.Fatalf("applying without starting scheduled a launch: %+v", stored)
		}
	}
}

func TestAutoStartFailureIsRecordedAndNotRetried(t *testing.T) {
	t.Parallel()

	fixture := newTaskAgentFixture(t)
	fixture.adapter.failAfter(1)
	fixture.writeTemplate(t, "chain", `{
	  "name": "chain",
	  "tasks": [
	    {"key": "first", "title": "First", "agent": "fake-agent", "auto_start": true, "auto_review": false},
	    {"key": "second", "title": "Second", "agent": "fake-agent", "auto_start": true, "auto_review": false}
	  ]
	}`)

	var applied daemon.AppliedTemplate
	fixture.mustCall(t, "template.apply", map[string]any{
		"workspace_id": fixture.workspace.ID, "name": "chain", "start": true,
	}, &applied)

	first := fixture.waitForAutoStart(t, applied.Tasks[0].ID)
	if first.AssigneeAgentID == "" {
		t.Fatalf("the first task was not started: %+v", first)
	}
	failed := fixture.waitForAutoStart(t, applied.Tasks[1].ID)
	if failed.AutoStartError == "" {
		t.Fatalf("the failed launch was not recorded: %+v", failed)
	}
	if failed.AssigneeAgentID != "" {
		t.Fatalf("a failed launch still assigned the task: %+v", failed)
	}
	if !strings.Contains(failed.AutoStartError, "auto-start") {
		t.Fatalf("the failure does not name the operation: %q", failed.AutoStartError)
	}
}

func TestSetAutoStartFromAClient(t *testing.T) {
	t.Parallel()

	fixture := newTaskAgentFixture(t)

	var dependency workflow.Task
	fixture.mustCall(t, "task.create", map[string]any{
		"workspace_id": fixture.workspace.ID, "title": "Dependency", "auto_review": false,
	}, &dependency)

	var task workflow.Task
	fixture.mustCall(t, "task.create", map[string]any{
		"workspace_id": fixture.workspace.ID, "title": "Dependent", "auto_review": false,
		"depends_on": []string{dependency.ID},
	}, &task)

	var updated workflow.Task
	fixture.mustCall(t, "task.setAutoStart", map[string]any{
		"task_id": task.ID, "agent": "fake-agent", "prompt": "work on it",
	}, &updated)
	if !updated.AutoStart || updated.AutoAgent != "fake-agent" || updated.AutoPrompt != "work on it" {
		t.Fatalf("auto-start was not recorded: %+v", updated)
	}

	if blocked := fixture.task(t, task.ID); blocked.AssigneeAgentID != "" {
		t.Fatal("a blocked task was started")
	}
	if err := fixture.call(t, "task.setAutoStart", map[string]any{
		"task_id": task.ID, "agent": "not-registered",
	}, &workflow.Task{}); err == nil {
		t.Fatal("an unregistered adapter was accepted")
	}

	var cleared workflow.Task
	fixture.mustCall(t, "task.setAutoStart", map[string]any{"task_id": task.ID}, &cleared)
	if cleared.AutoStart || cleared.AutoAgent != "" {
		t.Fatalf("auto-start was not cleared: %+v", cleared)
	}

	fixture.mustCall(t, "task.setAutoStart", map[string]any{
		"task_id": task.ID, "agent": "fake-agent",
	}, &updated)
	fixture.mustCall(t, "task.setStatus", map[string]any{
		"task_id": dependency.ID, "status": "done",
	}, &dependency)

	started := fixture.waitForAutoStart(t, task.ID)
	if started.AssigneeAgentID == "" {
		t.Fatalf("the task was not started when its dependency finished: %+v", started)
	}
}
