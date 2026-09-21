package workflow_test

import (
	"testing"

	"github.com/martintrifunov/orkestar/internal/workflow"
)

func TestSetAutoStartStoresAndClears(t *testing.T) {
	t.Parallel()

	board := workflow.NewBoard()
	task := mustCreate(t, board, "ship the feature")

	updated, err := board.SetAutoStart(task.ID, "claude-code", "work on it")
	if err != nil {
		t.Fatalf("set auto-start: %v", err)
	}
	if !updated.AutoStart || updated.AutoAgent != "claude-code" || updated.AutoPrompt != "work on it" {
		t.Fatalf("auto-start was not recorded: %+v", updated)
	}

	stored, err := board.Get(task.ID)
	if err != nil {
		t.Fatalf("get task: %v", err)
	}
	if !stored.AutoStart || stored.AutoAgent != "claude-code" {
		t.Fatalf("auto-start did not round-trip: %+v", stored)
	}

	cleared, err := board.SetAutoStart(task.ID, "", "")
	if err != nil {
		t.Fatalf("clear auto-start: %v", err)
	}
	if cleared.AutoStart || cleared.AutoAgent != "" || cleared.AutoPrompt != "" {
		t.Fatalf("auto-start was not cleared: %+v", cleared)
	}
}

func TestSetAutoStartClearsAPreviousError(t *testing.T) {
	t.Parallel()

	board := workflow.NewBoard()
	task := mustCreate(t, board, "ship the feature")

	if _, err := board.SetAutoStart(task.ID, "claude-code", ""); err != nil {
		t.Fatalf("set auto-start: %v", err)
	}
	failed, err := board.SetAutoStartError(task.ID, "no such adapter")
	if err != nil {
		t.Fatalf("record failure: %v", err)
	}
	if failed.AutoStartError == "" {
		t.Fatal("the failure reason was not recorded")
	}

	retried, err := board.SetAutoStart(task.ID, "claude-code", "")
	if err != nil {
		t.Fatalf("set auto-start again: %v", err)
	}
	if retried.AutoStartError != "" {
		t.Fatalf("a fresh request kept the old failure: %+v", retried)
	}
}

func TestSetAutoStartRejectsAMissingTask(t *testing.T) {
	t.Parallel()

	board := workflow.NewBoard()
	if _, err := board.SetAutoStart("task_missing", "claude-code", ""); err == nil {
		t.Fatal("expected an error for a task that does not exist")
	}
	if _, err := board.SetAutoStartError("task_missing", "reason"); err == nil {
		t.Fatal("expected an error for a task that does not exist")
	}
}

func TestTemplateRejectsAutoStartWithoutAnAgent(t *testing.T) {
	t.Parallel()

	_, err := workflow.ParseTemplate([]byte(`{
		"name": "bad",
		"tasks": [{"key": "one", "title": "One", "auto_start": true}]
	}`))
	if err == nil {
		t.Fatal("expected auto_start without an agent to be rejected")
	}
}

func TestTemplateAcceptsAutoStartWithAnAgent(t *testing.T) {
	t.Parallel()

	template, err := workflow.ParseTemplate([]byte(`{
		"name": "chain",
		"tasks": [
			{"key": "one", "title": "One", "agent": "claude-code", "auto_start": true},
			{"key": "two", "title": "Two", "agent": "claude-code", "auto_start": true, "depends_on": ["one"]}
		]
	}`))
	if err != nil {
		t.Fatalf("parse template: %v", err)
	}
	if !template.Tasks[0].AutoStart || !template.Tasks[1].AutoStart {
		t.Fatalf("auto_start was not parsed: %+v", template.Tasks)
	}
}
