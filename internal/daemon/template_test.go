package daemon_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/martintrifunov/orkestar/internal/daemon"
	"github.com/martintrifunov/orkestar/internal/workflow"
)

// writeTemplate puts a template where a workspace keeps them, which is inside
// the workspace so it can be committed beside the code it describes.
func (f taskAgentFixture) writeTemplate(t *testing.T, name, body string) {
	t.Helper()
	directory := filepath.Join(f.directory, ".orkestar", "templates")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatalf("create template directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, name+".json"), []byte(body), 0o644); err != nil {
		t.Fatalf("write template: %v", err)
	}
}

const pipeline = `{
  "name": "pipeline",
  "tasks": [
    {"key": "review", "title": "Review the change", "depends_on": ["build"], "auto_review": false},
    {"key": "build", "title": "Build it", "worktree": true, "auto_review": false, "agent": "fake-agent"}
  ]
}`

// A pipeline declared once and applied whenever it is needed, with the
// dependency between its tasks preserved through keys that never leave the
// file.
func TestApplyTemplateCreatesTheWholePipeline(t *testing.T) {
	t.Parallel()

	fixture := newTaskAgentFixture(t)
	fixture.writeTemplate(t, "pipeline", pipeline)

	var applied daemon.AppliedTemplate
	fixture.mustCall(t, "template.apply", map[string]any{
		"workspace_id": fixture.workspace.ID, "name": "pipeline",
	}, &applied)

	if len(applied.Tasks) != 2 {
		t.Fatalf("created %d tasks", len(applied.Tasks))
	}
	// Declared second but depended on, so created first.
	build, review := applied.Tasks[0], applied.Tasks[1]
	if build.Title != "Build it" || review.Title != "Review the change" {
		t.Fatalf("unexpected order: %q then %q", build.Title, review.Title)
	}
	if len(review.DependsOn) != 1 || review.DependsOn[0] != build.ID {
		t.Fatalf("the dependency was not resolved to an ID: %v", review.DependsOn)
	}
	if build.WorktreePath == "" {
		t.Fatal("the task that asked for a worktree did not get one")
	}
	if review.WorktreePath != "" {
		t.Fatal("a task that did not ask for a worktree got one")
	}
	// Nothing was started, because nothing asked for it.
	if len(applied.Agents) != 0 {
		t.Fatalf("agents were started without being asked: %v", applied.Agents)
	}
	if _, err := fixture.callTaskStatus(t, review.ID, "in_progress"); err == nil {
		t.Fatal("the dependent task is not actually blocked")
	}
}

// Applying with starting enabled launches the agents the template names, on
// the tasks that can actually begin.
func TestApplyTemplateStartsOnlyWhatCanBegin(t *testing.T) {
	t.Parallel()

	fixture := newTaskAgentFixture(t)
	fixture.writeTemplate(t, "both", `{
	  "name": "both",
	  "tasks": [
	    {"key": "first", "title": "First", "auto_review": false, "agent": "fake-agent"},
	    {"key": "second", "title": "Second", "depends_on": ["first"], "auto_review": false, "agent": "fake-agent"}
	  ]
	}`)

	var applied daemon.AppliedTemplate
	fixture.mustCall(t, "template.apply", map[string]any{
		"workspace_id": fixture.workspace.ID, "name": "both", "start": true,
	}, &applied)

	if len(applied.Agents) != 1 {
		t.Fatalf("started %d agents, want the one that could begin", len(applied.Agents))
	}
	if applied.Agents[0].TaskID != applied.Tasks[0].ID {
		t.Fatalf("the agent is on %q, want the first task", applied.Agents[0].TaskID)
	}
	// The blocked one is reported rather than started, because an agent
	// launched against work it cannot begin would sit idle.
	if len(applied.Waiting) != 1 || applied.Waiting[0] != applied.Tasks[1].ID {
		t.Fatalf("the blocked task was not reported as waiting: %v", applied.Waiting)
	}
}

// A template that names an adapter nobody registered must fail before it
// creates anything, or it leaves half a pipeline behind.
func TestApplyTemplateChecksAdaptersBeforeCreatingTasks(t *testing.T) {
	t.Parallel()

	fixture := newTaskAgentFixture(t)
	fixture.writeTemplate(t, "ghost", `{
	  "name": "ghost",
	  "tasks": [{"key": "a", "title": "A", "auto_review": false, "agent": "not-installed"}]
	}`)

	var applied daemon.AppliedTemplate
	if err := fixture.call(t, "template.apply", map[string]any{
		"workspace_id": fixture.workspace.ID, "name": "ghost", "start": true,
	}, &applied); err == nil {
		t.Fatal("an unregistered adapter was accepted")
	}

	var snapshot daemon.Snapshot
	fixture.mustCall(t, "system.snapshot", nil, &snapshot)
	if len(snapshot.Tasks) != 0 {
		t.Fatalf("the failed apply left %d tasks behind", len(snapshot.Tasks))
	}
}

// Without starting, the same template applies fine: the adapter is only
// needed when something is actually launched.
func TestApplyTemplateWithoutStartingIgnoresAdapters(t *testing.T) {
	t.Parallel()

	fixture := newTaskAgentFixture(t)
	fixture.writeTemplate(t, "ghost", `{
	  "name": "ghost",
	  "tasks": [{"key": "a", "title": "A", "auto_review": false, "agent": "not-installed"}]
	}`)

	var applied daemon.AppliedTemplate
	fixture.mustCall(t, "template.apply", map[string]any{
		"workspace_id": fixture.workspace.ID, "name": "ghost",
	}, &applied)
	if len(applied.Tasks) != 1 {
		t.Fatalf("created %d tasks", len(applied.Tasks))
	}
}

func TestListTemplates(t *testing.T) {
	t.Parallel()

	fixture := newTaskAgentFixture(t)

	// A workspace with no templates has none, which is not a failure.
	var listed struct {
		Templates []workflow.Template `json:"templates"`
	}
	fixture.mustCall(t, "template.list", map[string]any{"workspace_id": fixture.workspace.ID}, &listed)
	if len(listed.Templates) != 0 {
		t.Fatalf("found %d templates in an empty workspace", len(listed.Templates))
	}

	fixture.writeTemplate(t, "pipeline", pipeline)
	fixture.writeTemplate(t, "broken", `{"name": "broken"}`)
	fixture.mustCall(t, "template.list", map[string]any{"workspace_id": fixture.workspace.ID}, &listed)
	if len(listed.Templates) != 2 {
		t.Fatalf("listed %d templates: %+v", len(listed.Templates), listed.Templates)
	}
	// Sorted, so the listing does not shuffle between calls.
	if listed.Templates[0].Name != "broken" || listed.Templates[1].Name != "pipeline" {
		t.Fatalf("unexpected order: %+v", listed.Templates)
	}
	// One unusable file must not hide the rest, and must say why.
	if listed.Templates[0].Description == "" {
		t.Fatal("the broken template does not explain itself")
	}
}

// The name crosses an IPC boundary and an agent may supply it, so it must not
// be able to reach a file outside the template directory.
func TestTemplateNamesCannotEscapeTheWorkspace(t *testing.T) {
	t.Parallel()

	fixture := newTaskAgentFixture(t)
	secret := filepath.Join(fixture.directory, "secret.json")
	if err := os.WriteFile(secret, []byte(`{"name":"secret","tasks":[{"key":"a","title":"A"}]}`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	for _, name := range []string{
		"../secret", "../../secret", "..", ".", "/etc/passwd",
		`..\secret`, "sub/secret",
	} {
		var applied daemon.AppliedTemplate
		if err := fixture.call(t, "template.apply", map[string]any{
			"workspace_id": fixture.workspace.ID, "name": name,
		}, &applied); err == nil {
			t.Fatalf("the name %q was accepted", name)
		}
	}
}

func TestApplyRejectsAnUnknownTemplate(t *testing.T) {
	t.Parallel()

	fixture := newTaskAgentFixture(t)
	var applied daemon.AppliedTemplate
	if err := fixture.call(t, "template.apply", map[string]any{
		"workspace_id": fixture.workspace.ID, "name": "nothing-here",
	}, &applied); err == nil {
		t.Fatal("applying a template that does not exist should fail")
	}
}

// callTaskStatus is a status change that is allowed to fail, for checking that
// a dependency actually blocks.
func (f taskAgentFixture) callTaskStatus(t *testing.T, taskID, status string) (workflow.Task, error) {
	t.Helper()
	var task workflow.Task
	err := f.call(t, "task.setStatus", map[string]any{"task_id": taskID, "status": status}, &task)
	return task, err
}

// Creating a workspace for a directory that already has one returns that one.
// A caller cannot know whether it exists, and an agent delegating work calls
// this before every task, so creating a second would scatter the work across
// two workspaces that mean the same directory.
func TestWorkspaceCreateReusesByDirectory(t *testing.T) {
	t.Parallel()

	fixture := newTaskAgentFixture(t)
	var again daemon.Workspace
	fixture.mustCall(t, "workspace.create", map[string]string{"directory": fixture.directory}, &again)
	if again.ID != fixture.workspace.ID {
		t.Fatalf("a second call made workspace %q, want %q", again.ID, fixture.workspace.ID)
	}

	// A different name does not fork it either: the tasks are already on the
	// one that exists.
	var named daemon.Workspace
	fixture.mustCall(t, "workspace.create", map[string]string{
		"directory": fixture.directory, "name": "something-else",
	}, &named)
	if named.ID != fixture.workspace.ID {
		t.Fatalf("naming it made workspace %q", named.ID)
	}

	var snapshot daemon.Snapshot
	fixture.mustCall(t, "system.snapshot", nil, &snapshot)
	if len(snapshot.Workspaces) != 1 {
		t.Fatalf("%d workspaces exist for one directory", len(snapshot.Workspaces))
	}

	// A genuinely different directory still gets its own.
	other := t.TempDir()
	var separate daemon.Workspace
	fixture.mustCall(t, "workspace.create", map[string]string{"directory": other}, &separate)
	if separate.ID == fixture.workspace.ID {
		t.Fatal("a different directory reused an unrelated workspace")
	}
}
