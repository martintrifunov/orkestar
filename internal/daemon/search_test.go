package daemon_test

import (
	"testing"
	"time"

	"github.com/martintrifunov/orkestar/internal/daemon"
	"github.com/martintrifunov/orkestar/internal/workflow"
)

type searchOutput struct {
	Query   string                `json:"query"`
	Results []daemon.SearchResult `json:"results"`
}

func TestSearchFindsPanesTasksAndArtifacts(t *testing.T) {
	t.Parallel()

	fixture := newTaskAgentFixture(t)

	var terminal daemon.Terminal
	fixture.mustCall(t, "terminal.start", map[string]any{
		"workspace_id": fixture.workspace.ID,
		"command":      []string{"/bin/sh", "-c", "printf 'needle-in-a-pane\\n'; sleep 30"},
	}, &terminal)

	task := fixture.createTask(t, "needle in a task")
	var artifact workflow.Artifact
	fixture.mustCall(t, "artifact.create", map[string]any{
		"task_id": task.ID, "kind": "log", "label": "needle log", "content": "a needle in the content",
	}, &artifact)

	deadline := time.Now().Add(5 * time.Second)
	for {
		var output searchOutput
		fixture.mustCall(t, "search.query", map[string]any{"query": "needle"}, &output)
		kinds := map[string]bool{}
		for _, result := range output.Results {
			kinds[result.Kind] = true
		}
		if kinds["terminal"] && kinds["task"] && kinds["artifact"] {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("search did not find everything: %+v", output.Results)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestSearchFiltersToAWorkspace(t *testing.T) {
	t.Parallel()

	fixture := newTaskAgentFixture(t)
	first := fixture.createTask(t, "needle here")

	var other daemon.Workspace
	fixture.mustCall(t, "workspace.create", map[string]string{"directory": t.TempDir()}, &other)
	var second workflow.Task
	fixture.mustCall(t, "task.create", map[string]any{
		"workspace_id": other.ID, "title": "needle there", "auto_review": false,
	}, &second)

	var output searchOutput
	fixture.mustCall(t, "search.query", map[string]any{
		"query": "needle", "workspace_id": other.ID,
	}, &output)
	if len(output.Results) == 0 {
		t.Fatal("the workspace filter dropped everything")
	}
	for _, result := range output.Results {
		if result.ID != second.ID {
			t.Fatalf("a result from another workspace was returned: %+v (first %s)", result, first.ID)
		}
	}
}

func TestSearchRejectsAnEmptyQuery(t *testing.T) {
	t.Parallel()

	fixture := newTaskAgentFixture(t)
	if err := fixture.call(t, "search.query", map[string]any{"query": "   "}, &searchOutput{}); err == nil {
		t.Fatal("an empty query was accepted")
	}
}
