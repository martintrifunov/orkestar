package workflow_test

import (
	"testing"

	"github.com/martintrifunov/orkestar/internal/workflow"
)

func TestArtifactAddRejectsMissingTaskOrInvalidKind(t *testing.T) {
	t.Parallel()

	store := workflow.NewArtifactStore()
	if _, err := store.Add("", workflow.ArtifactDiff, "diff", "", "content"); err == nil {
		t.Fatal("expected add to fail without a task ID")
	}
	if _, err := store.Add("task_1", workflow.ArtifactKind("bogus"), "diff", "", "content"); err == nil {
		t.Fatal("expected add to fail for an invalid kind")
	}
}

func TestArtifactAddGetAndForTask(t *testing.T) {
	t.Parallel()

	store := workflow.NewArtifactStore()
	diff, err := store.Add("task_1", workflow.ArtifactDiff, "changes", "", "--- a\n+++ b\n")
	if err != nil {
		t.Fatalf("add diff: %v", err)
	}
	testResult, err := store.Add("task_1", workflow.ArtifactTestResult, "go test", "/tmp/out.log", "")
	if err != nil {
		t.Fatalf("add test result: %v", err)
	}
	if _, err := store.Add("task_2", workflow.ArtifactLog, "unrelated", "", "noise"); err != nil {
		t.Fatalf("add unrelated artifact: %v", err)
	}

	fetched, err := store.Get(diff.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if fetched.Content != "--- a\n+++ b\n" {
		t.Fatalf("unexpected content: %q", fetched.Content)
	}

	forTask := store.ForTask("task_1")
	if len(forTask) != 2 || forTask[0].ID != diff.ID || forTask[1].ID != testResult.ID {
		t.Fatalf("unexpected artifacts for task_1: %#v", forTask)
	}

	all := store.List()
	if len(all) != 3 {
		t.Fatalf("unexpected total artifact count: %d", len(all))
	}
}

func TestArtifactGetMissing(t *testing.T) {
	t.Parallel()

	store := workflow.NewArtifactStore()
	if _, err := store.Get("missing"); err == nil {
		t.Fatal("expected get to fail for a missing artifact")
	}
}
