package daemon_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/martintrifunov/orkestar/internal/daemon"
	"github.com/martintrifunov/orkestar/internal/ipc"
	"github.com/martintrifunov/orkestar/internal/workflow"
)

func initRepo(t *testing.T) string {
	t.Helper()

	directory := t.TempDir()
	run := func(args ...string) {
		command := exec.Command("git", append([]string{"-C", directory}, args...)...)
		command.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
		)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	run("init", "-b", "main")
	if err := os.WriteFile(filepath.Join(directory, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	run("add", "README.md")
	run("commit", "-m", "initial commit")
	return directory
}

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

func TestTaskWorktreeCreateAndRemove(t *testing.T) {
	t.Parallel()

	repoDirectory := initRepo(t)
	socketDirectory, err := os.MkdirTemp("/tmp", "orkestar-task-worktree-test-")
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
	callContext, callCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer callCancel()

	var workspace daemon.Workspace
	if err := client.Call(callContext, "workspace.create", map[string]string{
		"directory": repoDirectory,
	}, &workspace); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	var task workflow.Task
	if err := client.Call(callContext, "task.create", map[string]any{
		"workspace_id": workspace.ID,
		"title":        "isolated work",
	}, &task); err != nil {
		t.Fatalf("create task: %v", err)
	}

	var withWorktree workflow.Task
	if err := client.Call(callContext, "task.createWorktree", map[string]string{
		"task_id": task.ID,
	}, &withWorktree); err != nil {
		t.Fatalf("create task worktree: %v", err)
	}
	if withWorktree.WorktreePath == "" || withWorktree.WorktreeBranch == "" {
		t.Fatalf("expected worktree metadata to be set: %#v", withWorktree)
	}
	if _, err := os.Stat(filepath.Join(withWorktree.WorktreePath, "README.md")); err != nil {
		t.Fatalf("expected worktree checkout on disk: %v", err)
	}

	var cleared workflow.Task
	if err := client.Call(callContext, "task.removeWorktree", map[string]string{
		"task_id": task.ID,
	}, &cleared); err != nil {
		t.Fatalf("remove task worktree: %v", err)
	}
	if cleared.WorktreePath != "" || cleared.WorktreeBranch != "" {
		t.Fatalf("expected worktree metadata to be cleared: %#v", cleared)
	}
	if _, err := os.Stat(withWorktree.WorktreePath); !os.IsNotExist(err) {
		t.Fatalf("expected worktree directory to be removed, stat err: %v", err)
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
