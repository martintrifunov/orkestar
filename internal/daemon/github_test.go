package daemon_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/martintrifunov/orkestar/internal/daemon"
	"github.com/martintrifunov/orkestar/internal/workflow"
)

// writeFakeGH puts a gh on PATH that answers the two calls Orkestar makes,
// so the integration is tested without a GitHub account or a network.
func writeFakeGH(t *testing.T, directory string) {
	t.Helper()
	script := `#!/bin/sh
case "$1" in
issue)
  printf '{"number":42,"title":"Imported issue","body":"From GitHub","url":"https://example.test/issues/42"}'
  ;;
pr)
  printf 'https://example.test/pull/7\n'
  ;;
*)
  echo "unexpected gh invocation: $*" >&2
  exit 1
  ;;
esac
`
	if err := os.WriteFile(filepath.Join(directory, "gh"), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake gh: %v", err)
	}
}

func TestGitHubImportCreatesATask(t *testing.T) {
	fixture := newTaskAgentFixture(t)
	bin := t.TempDir()
	writeFakeGH(t, bin)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	var imported daemon.GitHubImport
	fixture.mustCall(t, "task.githubImport", map[string]any{
		"workspace_id": fixture.workspace.ID, "reference": "42",
	}, &imported)

	if imported.Task.Title != "Imported issue" || imported.Number != 42 {
		t.Fatalf("unexpected import: %+v", imported)
	}
	if !strings.Contains(imported.Task.Description, "https://example.test/issues/42") {
		t.Fatalf("the issue URL is not in the description: %q", imported.Task.Description)
	}
}

func TestGitHubImportWithoutGHChangesNothing(t *testing.T) {
	fixture := newTaskAgentFixture(t)

	var before daemon.Snapshot
	fixture.mustCall(t, "system.snapshot", nil, &before)

	// A PATH with no gh on it: the call must fail before creating a task.
	t.Setenv("PATH", t.TempDir())
	if err := fixture.call(t, "task.githubImport", map[string]any{
		"workspace_id": fixture.workspace.ID, "reference": "42",
	}, &daemon.GitHubImport{}); err == nil {
		t.Fatal("an import without gh should fail")
	}

	var after daemon.Snapshot
	fixture.mustCall(t, "system.snapshot", nil, &after)
	if len(after.Tasks) != len(before.Tasks) {
		t.Fatalf("a failed import created %d task(s)", len(after.Tasks)-len(before.Tasks))
	}
}

func TestGitHubPullRequestRecordsAnArtifact(t *testing.T) {
	fixture := newTaskAgentFixture(t)
	bin := t.TempDir()
	writeFakeGH(t, bin)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	task := fixture.createTask(t, "ship it")
	var withWorktree workflow.Task
	fixture.mustCall(t, "task.createWorktree", map[string]any{"task_id": task.ID}, &withWorktree)
	if withWorktree.WorktreeBranch == "" {
		t.Fatal("the task did not get a branch")
	}
	fixture.mustCall(t, "task.setStatus", map[string]any{
		"task_id": task.ID, "status": "done",
	}, &workflow.Task{})

	var artifact workflow.Artifact
	fixture.mustCall(t, "task.githubPR", map[string]any{"task_id": task.ID}, &artifact)
	if artifact.Kind != workflow.ArtifactPullRequest {
		t.Fatalf("unexpected artifact kind: %s", artifact.Kind)
	}
	if artifact.Path != "https://example.test/pull/7" {
		t.Fatalf("unexpected pull request URL: %q", artifact.Path)
	}
}

func TestGitHubPullRequestRefusesAnUnfinishedTask(t *testing.T) {
	fixture := newTaskAgentFixture(t)
	bin := t.TempDir()
	writeFakeGH(t, bin)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	task := fixture.createTask(t, "still running")
	var withWorktree workflow.Task
	fixture.mustCall(t, "task.createWorktree", map[string]any{"task_id": task.ID}, &withWorktree)

	if err := fixture.call(t, "task.githubPR", map[string]any{"task_id": task.ID}, &workflow.Artifact{}); err == nil {
		t.Fatal("an unfinished task should not open a pull request")
	}
}
