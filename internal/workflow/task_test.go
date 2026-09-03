package workflow_test

import (
	"testing"

	"github.com/martintrifunov/orkestar/internal/workflow"
)

func TestCreateRejectsMissingDependency(t *testing.T) {
	t.Parallel()

	board := workflow.NewBoard()
	if _, err := board.Create("ws", "build", "", []string{"missing"}); err == nil {
		t.Fatal("expected create to fail for a missing dependency")
	}
}

func TestSetStatusBlocksOnIncompleteDependency(t *testing.T) {
	t.Parallel()

	board := workflow.NewBoard()
	dependency, err := board.Create("ws", "write tests", "", nil)
	if err != nil {
		t.Fatalf("create dependency: %v", err)
	}
	task, err := board.Create("ws", "ship feature", "", []string{dependency.ID})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	if _, err := board.SetStatus(task.ID, workflow.StatusInProgress); err == nil {
		t.Fatal("expected transition to in_progress to be blocked")
	}

	if _, err := board.SetStatus(dependency.ID, workflow.StatusDone); err != nil {
		t.Fatalf("complete dependency: %v", err)
	}
	updated, err := board.SetStatus(task.ID, workflow.StatusInProgress)
	if err != nil {
		t.Fatalf("transition to in_progress after dependency done: %v", err)
	}
	if updated.Status != workflow.StatusInProgress {
		t.Fatalf("unexpected status: %q", updated.Status)
	}
}

func TestSetStatusRejectsUnknownValue(t *testing.T) {
	t.Parallel()

	board := workflow.NewBoard()
	task, err := board.Create("ws", "task", "", nil)
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if _, err := board.SetStatus(task.ID, workflow.Status("bogus")); err == nil {
		t.Fatal("expected unknown status to be rejected")
	}
}

func TestAssignAndList(t *testing.T) {
	t.Parallel()

	board := workflow.NewBoard()
	first, err := board.Create("ws", "first", "", nil)
	if err != nil {
		t.Fatalf("create first: %v", err)
	}
	second, err := board.Create("ws", "second", "", nil)
	if err != nil {
		t.Fatalf("create second: %v", err)
	}

	assigned, err := board.Assign(first.ID, "agent_1")
	if err != nil {
		t.Fatalf("assign: %v", err)
	}
	if assigned.AssigneeAgentID != "agent_1" {
		t.Fatalf("unexpected assignee: %q", assigned.AssigneeAgentID)
	}

	tasks := board.List()
	if len(tasks) != 2 || tasks[0].ID != first.ID || tasks[1].ID != second.ID {
		t.Fatalf("unexpected task order: %#v", tasks)
	}
}

func TestGetMissingTask(t *testing.T) {
	t.Parallel()

	board := workflow.NewBoard()
	if _, err := board.Get("does-not-exist"); err == nil {
		t.Fatal("expected get to fail for a missing task")
	}
}
