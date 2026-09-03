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

func TestTaskCreateDependencyAndAssignment(t *testing.T) {
	t.Parallel()

	temporaryDirectory := t.TempDir()
	socketDirectory, err := os.MkdirTemp("/tmp", "orkestar-task-test-")
	if err != nil {
		t.Fatalf("create socket directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDirectory) })
	socketPath := filepath.Join(socketDirectory, "orkestar.sock")

	server := daemon.NewServer(socketPath)
	ctx, cancel := context.WithCancel(context.Background())
	serverError := make(chan error, 1)
	go func() { serverError <- server.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-serverError; err != nil {
			t.Errorf("server shutdown: %v", err)
		}
	})

	client := ipc.NewClient(socketPath)
	waitForServer(t, client)
	callContext, callCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer callCancel()

	var workspace daemon.Workspace
	if err := client.Call(callContext, "workspace.create", map[string]string{
		"directory": temporaryDirectory,
	}, &workspace); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	var dependency workflow.Task
	if err := client.Call(callContext, "task.create", map[string]any{
		"workspace_id": workspace.ID,
		"title":        "write tests",
	}, &dependency); err != nil {
		t.Fatalf("create dependency task: %v", err)
	}

	var task workflow.Task
	if err := client.Call(callContext, "task.create", map[string]any{
		"workspace_id": workspace.ID,
		"title":        "ship feature",
		"depends_on":   []string{dependency.ID},
	}, &task); err != nil {
		t.Fatalf("create task: %v", err)
	}
	if task.Status != workflow.StatusPending {
		t.Fatalf("unexpected initial status: %q", task.Status)
	}

	if err := client.Call(callContext, "task.setStatus", map[string]string{
		"task_id": task.ID,
		"status":  string(workflow.StatusInProgress),
	}, &task); err == nil {
		t.Fatal("expected transition to in_progress to be blocked by dependency")
	}

	if err := client.Call(callContext, "task.setStatus", map[string]string{
		"task_id": dependency.ID,
		"status":  string(workflow.StatusDone),
	}, &dependency); err != nil {
		t.Fatalf("complete dependency: %v", err)
	}

	if err := client.Call(callContext, "task.setStatus", map[string]string{
		"task_id": task.ID,
		"status":  string(workflow.StatusInProgress),
	}, &task); err != nil {
		t.Fatalf("transition task after dependency done: %v", err)
	}
	if task.Status != workflow.StatusInProgress {
		t.Fatalf("unexpected status: %q", task.Status)
	}

	var assigned workflow.Task
	if err := client.Call(callContext, "task.assign", map[string]string{
		"task_id":  task.ID,
		"agent_id": "agent_1",
	}, &assigned); err != nil {
		t.Fatalf("assign task: %v", err)
	}
	if assigned.AssigneeAgentID != "agent_1" {
		t.Fatalf("unexpected assignee: %q", assigned.AssigneeAgentID)
	}

	var snapshot daemon.Snapshot
	if err := client.Call(callContext, "system.snapshot", nil, &snapshot); err != nil {
		t.Fatalf("get snapshot: %v", err)
	}
	if len(snapshot.Tasks) != 2 {
		t.Fatalf("unexpected tasks in snapshot: %#v", snapshot.Tasks)
	}
}

func TestTaskCreateRejectsUnknownWorkspace(t *testing.T) {
	t.Parallel()

	socketDirectory, err := os.MkdirTemp("/tmp", "orkestar-task-ws-test-")
	if err != nil {
		t.Fatalf("create socket directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDirectory) })
	socketPath := filepath.Join(socketDirectory, "orkestar.sock")

	server := daemon.NewServer(socketPath)
	ctx, cancel := context.WithCancel(context.Background())
	serverError := make(chan error, 1)
	go func() { serverError <- server.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-serverError; err != nil {
			t.Errorf("server shutdown: %v", err)
		}
	})

	client := ipc.NewClient(socketPath)
	waitForServer(t, client)
	callContext, callCancel := context.WithTimeout(context.Background(), time.Second)
	defer callCancel()

	var task workflow.Task
	if err := client.Call(callContext, "task.create", map[string]any{
		"workspace_id": "does-not-exist",
		"title":        "orphan",
	}, &task); err == nil {
		t.Fatal("expected task create to fail for an unknown workspace")
	}
}
