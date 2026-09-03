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

func TestArtifactCreateRequiresExistingTask(t *testing.T) {
	t.Parallel()

	temporaryDirectory := t.TempDir()
	socketDirectory, err := os.MkdirTemp("/tmp", "orkestar-artifact-test-")
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

	var artifact workflow.Artifact
	if err := client.Call(callContext, "artifact.create", map[string]string{
		"task_id": "missing",
		"kind":    string(workflow.ArtifactDiff),
		"content": "diff",
	}, &artifact); err == nil {
		t.Fatal("expected artifact create to fail for an unknown task")
	}

	var workspace daemon.Workspace
	if err := client.Call(callContext, "workspace.create", map[string]string{
		"directory": temporaryDirectory,
	}, &workspace); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	var task workflow.Task
	if err := client.Call(callContext, "task.create", map[string]any{
		"workspace_id": workspace.ID,
		"title":        "review this",
	}, &task); err != nil {
		t.Fatalf("create task: %v", err)
	}

	if err := client.Call(callContext, "artifact.create", map[string]string{
		"task_id": task.ID,
		"kind":    string(workflow.ArtifactDiff),
		"label":   "changes",
		"content": "--- a\n+++ b\n",
	}, &artifact); err != nil {
		t.Fatalf("create artifact: %v", err)
	}
	if artifact.TaskID != task.ID || artifact.Kind != workflow.ArtifactDiff {
		t.Fatalf("unexpected artifact: %#v", artifact)
	}

	var snapshot daemon.Snapshot
	if err := client.Call(callContext, "system.snapshot", nil, &snapshot); err != nil {
		t.Fatalf("get snapshot: %v", err)
	}
	if len(snapshot.Artifacts) != 1 || snapshot.Artifacts[0].ID != artifact.ID {
		t.Fatalf("unexpected artifacts in snapshot: %#v", snapshot.Artifacts)
	}
}
