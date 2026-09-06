package workflow_test

import (
	"strings"
	"testing"

	"github.com/martintrifunov/orkestar/internal/workflow"
)

func text(s string) *string     { return &s }
func ids(v ...string) *[]string { return &v }
func mustCreate(t *testing.T, b *workflow.Board, title string, dependsOn ...string) workflow.Task {
	t.Helper()
	task, err := b.Create("workspace", title, "", dependsOn, false)
	if err != nil {
		t.Fatalf("create %q: %v", title, err)
	}
	return task
}

func TestUpdateChangesOnlyWhatItIsGiven(t *testing.T) {
	board := workflow.NewBoard()
	created, err := board.Create("workspace", "Original", "Original description", nil, false)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	retitled, err := board.Update(created.ID, workflow.TaskEdit{Title: text("Renamed")})
	if err != nil {
		t.Fatalf("update title: %v", err)
	}
	if retitled.Title != "Renamed" {
		t.Fatalf("title is %q", retitled.Title)
	}
	// The field that was not sent must survive: a caller renaming a task is
	// not saying the description should go.
	if retitled.Description != "Original description" {
		t.Fatalf("description was lost: %q", retitled.Description)
	}
	if !retitled.UpdatedAt.After(created.UpdatedAt) && !retitled.UpdatedAt.Equal(created.UpdatedAt) {
		t.Fatal("UpdatedAt went backwards")
	}

	described, err := board.Update(created.ID, workflow.TaskEdit{Description: text("Rewritten")})
	if err != nil {
		t.Fatalf("update description: %v", err)
	}
	if described.Title != "Renamed" || described.Description != "Rewritten" {
		t.Fatalf("unexpected task: %+v", described)
	}

	// An explicit empty description is a real value, not an absent one.
	cleared, err := board.Update(created.ID, workflow.TaskEdit{Description: text("")})
	if err != nil {
		t.Fatalf("clear description: %v", err)
	}
	if cleared.Description != "" {
		t.Fatalf("description was not cleared: %q", cleared.Description)
	}
}

func TestUpdateRejectsAnEmptyTitle(t *testing.T) {
	board := workflow.NewBoard()
	task := mustCreate(t, board, "Has a title")
	if _, err := board.Update(task.ID, workflow.TaskEdit{Title: text("")}); err == nil {
		t.Fatal("an empty title should be rejected")
	}
	if after, _ := board.Get(task.ID); after.Title != "Has a title" {
		t.Fatalf("the rejected edit was applied anyway: %q", after.Title)
	}
}

func TestUpdateSetsDependenciesDiscoveredLater(t *testing.T) {
	board := workflow.NewBoard()
	first := mustCreate(t, board, "First")
	second := mustCreate(t, board, "Second")

	updated, err := board.Update(second.ID, workflow.TaskEdit{DependsOn: ids(first.ID)})
	if err != nil {
		t.Fatalf("add dependency: %v", err)
	}
	if len(updated.DependsOn) != 1 || updated.DependsOn[0] != first.ID {
		t.Fatalf("dependencies are %v", updated.DependsOn)
	}
	if _, err := board.SetStatus(second.ID, workflow.StatusInProgress); err == nil {
		t.Fatal("the new dependency does not block starting")
	}

	// And they can be taken away again.
	cleared, err := board.Update(second.ID, workflow.TaskEdit{DependsOn: ids()})
	if err != nil {
		t.Fatalf("clear dependencies: %v", err)
	}
	if len(cleared.DependsOn) != 0 {
		t.Fatalf("dependencies are %v", cleared.DependsOn)
	}
	if _, err := board.SetStatus(second.ID, workflow.StatusInProgress); err != nil {
		t.Fatalf("clearing dependencies did not unblock: %v", err)
	}
}

// Create cannot build a cycle, because a task may only depend on tasks that
// already exist. An edit can, and every task in one would wait forever.
func TestUpdateRejectsCycles(t *testing.T) {
	board := workflow.NewBoard()
	first := mustCreate(t, board, "First")
	second := mustCreate(t, board, "Second", first.ID)
	third := mustCreate(t, board, "Third", second.ID)

	t.Run("direct", func(t *testing.T) {
		_, err := board.Update(first.ID, workflow.TaskEdit{DependsOn: ids(second.ID)})
		if err == nil {
			t.Fatal("a two-task cycle was accepted")
		}
		if !strings.Contains(err.Error(), "startable") {
			t.Fatalf("the error does not say why: %v", err)
		}
	})

	t.Run("indirect", func(t *testing.T) {
		if _, err := board.Update(first.ID, workflow.TaskEdit{DependsOn: ids(third.ID)}); err == nil {
			t.Fatal("a three-task cycle was accepted")
		}
	})

	t.Run("self", func(t *testing.T) {
		if _, err := board.Update(first.ID, workflow.TaskEdit{DependsOn: ids(first.ID)}); err == nil {
			t.Fatal("a task was allowed to depend on itself")
		}
	})

	// A rejected edit must leave the board exactly as it was.
	if after, _ := board.Get(first.ID); len(after.DependsOn) != 0 {
		t.Fatalf("a rejected edit changed the task: %v", after.DependsOn)
	}
	if _, err := board.SetStatus(first.ID, workflow.StatusInProgress); err != nil {
		t.Fatalf("the first task is no longer startable: %v", err)
	}
}

// A diamond is not a cycle: two tasks may both depend on the same one, and
// both be depended on by a third.
func TestUpdateAllowsSharedDependencies(t *testing.T) {
	board := workflow.NewBoard()
	base := mustCreate(t, board, "Base")
	left := mustCreate(t, board, "Left", base.ID)
	right := mustCreate(t, board, "Right", base.ID)
	top := mustCreate(t, board, "Top")

	if _, err := board.Update(top.ID, workflow.TaskEdit{DependsOn: ids(left.ID, right.ID)}); err != nil {
		t.Fatalf("a diamond was rejected: %v", err)
	}
}

func TestUpdateRejectsAnUnknownDependency(t *testing.T) {
	board := workflow.NewBoard()
	task := mustCreate(t, board, "Only task")
	if _, err := board.Update(task.ID, workflow.TaskEdit{DependsOn: ids("task_missing")}); err == nil {
		t.Fatal("a dependency that does not exist was accepted")
	}
}

func TestUpdateDeduplicatesDependencies(t *testing.T) {
	board := workflow.NewBoard()
	first := mustCreate(t, board, "First")
	second := mustCreate(t, board, "Second")

	updated, err := board.Update(second.ID, workflow.TaskEdit{DependsOn: ids(first.ID, first.ID)})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if len(updated.DependsOn) != 1 {
		t.Fatalf("dependencies are %v, want one", updated.DependsOn)
	}
}

func TestUpdateRejectsAnUnknownTask(t *testing.T) {
	board := workflow.NewBoard()
	if _, err := board.Update("task_missing", workflow.TaskEdit{Title: text("New")}); err == nil {
		t.Fatal("updating a task that does not exist should fail")
	}
}
