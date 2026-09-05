package daemon_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/martintrifunov/orkestar/internal/agent/claude"
	"github.com/martintrifunov/orkestar/internal/daemon"
	"github.com/martintrifunov/orkestar/internal/ipc"
)

// startResetServer brings up an isolated daemon with one registered adapter.
func startResetServer(t *testing.T) *ipc.Client {
	t.Helper()
	socketDirectory, err := os.MkdirTemp("/tmp", "orkestar-reset-test-")
	if err != nil {
		t.Fatalf("create socket directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDirectory) })

	server := daemon.NewServer(filepath.Join(socketDirectory, "orkestar.sock"))
	server.RegisterAdapter(claude.New(fixtureAgentExecutable(t)))
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
	return client
}

func TestResetClearsEverythingButKeepsAdaptersAndFiles(t *testing.T) {
	t.Parallel()
	client := startResetServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// A repository so the task can have a real worktree on disk.
	root := t.TempDir()
	for _, args := range [][]string{
		{"init"}, {"config", "user.name", "F"}, {"config", "user.email", "f@example.invalid"},
		{"commit", "--allow-empty", "-m", "Initial"},
	} {
		if b, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, b)
		}
	}

	var workspace daemon.Workspace
	if err := client.Call(ctx, "workspace.create", map[string]string{"directory": root}, &workspace); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	var running daemon.Terminal
	if err := client.Call(ctx, "terminal.start", map[string]any{
		"workspace_id": workspace.ID,
		"command":      []string{"/bin/sh", "-c", "while :; do sleep 1; done"},
		"columns":      80, "rows": 24,
	}, &running); err != nil {
		t.Fatalf("start terminal: %v", err)
	}
	var task struct {
		ID           string `json:"id"`
		WorktreePath string `json:"worktree_path"`
	}
	if err := client.Call(ctx, "task.create", map[string]any{
		"workspace_id": workspace.ID, "title": "Some work",
	}, &task); err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := client.Call(ctx, "task.createWorktree", map[string]any{"task_id": task.ID}, &task); err != nil {
		t.Fatalf("create worktree: %v", err)
	}
	if task.WorktreePath == "" {
		t.Fatal("the task has no worktree to report")
	}
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(task.WorktreePath)) })

	// Without confirmation nothing is touched, so a stray call cannot wipe a
	// daemon full of work.
	var refused daemon.ResetSummary
	err := client.Call(ctx, "system.reset", map[string]any{}, &refused)
	if err == nil {
		t.Fatal("reset went ahead without confirmation")
	}
	var before daemon.Snapshot
	if err := client.Call(ctx, "system.snapshot", nil, &before); err != nil {
		t.Fatal(err)
	}
	if len(before.Terminals) != 1 || len(before.Tasks) != 1 || len(before.Workspaces) != 1 {
		t.Fatalf("the refused reset changed state: %+v", before)
	}

	var summary daemon.ResetSummary
	if err := client.Call(ctx, "system.reset", map[string]any{"confirm": true}, &summary); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if summary.Terminals != 1 || summary.Tasks != 1 || summary.Workspaces != 1 {
		t.Fatalf("reset reported the wrong counts: %+v", summary)
	}
	if len(summary.Worktrees) != 1 || summary.Worktrees[0] != task.WorktreePath {
		t.Fatalf("the worktree was not reported: %+v", summary.Worktrees)
	}

	var after daemon.Snapshot
	if err := client.Call(ctx, "system.snapshot", nil, &after); err != nil {
		t.Fatal(err)
	}
	if len(after.Terminals) != 0 || len(after.Agents) != 0 || len(after.Tasks) != 0 ||
		len(after.Workspaces) != 0 || len(after.Artifacts) != 0 || len(after.Permissions) != 0 {
		t.Fatalf("state survived the reset: %+v", after)
	}
	// Adapters are configuration, not state, so they stay registered.
	if len(after.Adapters) != 1 || after.Adapters[0].Name != "claude-code" {
		t.Fatalf("reset dropped the registered adapters: %+v", after.Adapters)
	}
	// Files on disk are never deleted by a reset.
	if _, err := os.Stat(task.WorktreePath); err != nil {
		t.Fatalf("reset deleted the task worktree: %v", err)
	}

	// The daemon is still usable, and a second reset is a no-op.
	var reused daemon.Workspace
	if err := client.Call(ctx, "workspace.create", map[string]string{"directory": root}, &reused); err != nil {
		t.Fatalf("daemon unusable after reset: %v", err)
	}
	var second daemon.ResetSummary
	if err := client.Call(ctx, "system.reset", map[string]any{"confirm": true}, &second); err != nil {
		t.Fatalf("second reset: %v", err)
	}
	if second.Terminals != 0 || second.Tasks != 0 || second.Workspaces != 1 {
		t.Fatalf("second reset reported stale counts: %+v", second)
	}
}
