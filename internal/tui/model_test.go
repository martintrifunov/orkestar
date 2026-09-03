package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/martintrifunov/orkestar/internal/daemon"
	"github.com/martintrifunov/orkestar/internal/workflow"
)

func sampleSnapshot() daemon.Snapshot {
	now := time.Now().UTC()
	return daemon.Snapshot{
		Workspaces: []daemon.Workspace{{ID: "w_1", Name: "orkestar", Directory: "/repo", CreatedAt: now}},
		Terminals:  []daemon.Terminal{{ID: "term_1", State: "running", Command: []string{"claude"}, CreatedAt: now}},
		Agents: []daemon.Agent{{
			ID: "agent_1", Adapter: "claude-code", Mode: "interactive",
			State: "waiting_permission", AttentionReason: "wants to run rm -rf", CreatedAt: now,
		}},
		Permissions: []daemon.PermissionRequest{{ID: "perm_1", AgentID: "agent_1", Reason: "wants to run rm -rf", CreatedAt: now}},
		Tasks: []workflow.Task{{
			ID: "task_1", WorkspaceID: "w_1", Title: "ship feature", Status: workflow.StatusInProgress,
			AutoReview: true, WorktreePath: "/repo-worktrees/task_1", WorktreeBranch: "task/task_1", CreatedAt: now,
		}},
		Artifacts: []workflow.Artifact{{
			ID: "artifact_1", TaskID: "task_1", Kind: workflow.ArtifactReview,
			Content: "VERDICT: REJECT\nMissing tests.", CreatedAt: now,
		}},
	}
}

func TestRenderShowsAgentsAndPermissions(t *testing.T) {
	model := Model{snapshot: sampleSnapshot(), width: 140, height: 40}
	output := model.render()

	for _, want := range []string{"claude-code", "waiting_permission", "wants to run rm -rf", "Pending permissions", "agent_1"} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected render output to contain %q, got:\n%s", want, output)
		}
	}
}

func TestRenderNarrowWidthStacksPanels(t *testing.T) {
	model := Model{snapshot: sampleSnapshot(), width: 60, height: 40}
	output := model.render()
	if !strings.Contains(output, "Agents") || !strings.Contains(output, "Tasks") {
		t.Fatalf("expected stacked layout to still include all panels, got:\n%s", output)
	}
}

func TestRenderDiffShowsChangedFilesAndReviewVerdict(t *testing.T) {
	model := Model{
		snapshot:    sampleSnapshot(),
		width:       140,
		height:      40,
		diffTaskID:  "task_1",
		viewingDiff: true,
		diff: daemon.TaskDiff{
			Diff: "diff --git a/x b/x\n+added line\n",
		},
	}
	output := model.render()

	for _, want := range []string{"added line", "Latest reviewer verdict", "VERDICT: REJECT", "Missing tests."} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected diff view to contain %q, got:\n%s", want, output)
		}
	}
}
