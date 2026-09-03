package tui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/martintrifunov/orkestar/internal/agent"
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
		Adapters: []agent.Capabilities{
			{Name: "claude-code", SupportsInteractive: true, SupportsPrompt: true, SupportsInterrupt: true},
			{Name: "opencode", SupportsInteractive: true, SupportsManaged: true, SupportsPrompt: true, SupportsInterrupt: true, SupportsResume: true},
		},
	}
}

func key(text string) tea.KeyPressMsg {
	return tea.KeyPressMsg{Text: text}
}

func specialKey(code rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: code}
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

func TestPressingAOpensAgentPicker(t *testing.T) {
	model := Model{snapshot: sampleSnapshot(), width: 140, height: 40}

	updated, cmd := model.Update(key("a"))
	next := updated.(Model)

	if !next.pickingAgent {
		t.Fatal("expected pressing 'a' to open the agent picker")
	}
	if cmd != nil {
		t.Fatal("expected opening the picker not to produce a command")
	}

	output := next.render()
	for _, want := range []string{"New agent", "claude-code", "interactive", "opencode"} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected picker render to contain %q, got:\n%s", want, output)
		}
	}
}

func TestAgentPickerNavigationAndCancel(t *testing.T) {
	model := Model{snapshot: sampleSnapshot(), width: 140, height: 40, pickingAgent: true}

	updated, _ := model.Update(specialKey(tea.KeyDown))
	model = updated.(Model)
	if model.agentPickerAt != 1 {
		t.Fatalf("expected down to move selection to 1, got %d", model.agentPickerAt)
	}

	updated, _ = model.Update(specialKey(tea.KeyDown))
	model = updated.(Model)
	if model.agentPickerAt != 1 {
		t.Fatalf("expected selection to clamp at the last adapter, got %d", model.agentPickerAt)
	}

	updated, _ = model.Update(specialKey(tea.KeyUp))
	model = updated.(Model)
	if model.agentPickerAt != 0 {
		t.Fatalf("expected up to move selection back to 0, got %d", model.agentPickerAt)
	}

	updated, cmd := model.Update(specialKey(tea.KeyEsc))
	model = updated.(Model)
	if model.pickingAgent {
		t.Fatal("expected esc to close the picker")
	}
	if cmd != nil {
		t.Fatal("expected esc not to produce a command")
	}
}

func TestAgentPickerEnterLaunchesAndClosesPicker(t *testing.T) {
	model := Model{
		client:    nil,
		directory: "/repo",
		snapshot:  sampleSnapshot(),
		width:     140, height: 40,
		pickingAgent:  true,
		agentPickerAt: 1,
	}

	updated, cmd := model.Update(specialKey(tea.KeyEnter))
	next := updated.(Model)

	if next.pickingAgent {
		t.Fatal("expected enter to close the picker")
	}
	if cmd == nil {
		t.Fatal("expected enter to produce a launch command")
	}
}

func TestPickingAgentInterceptsOtherKeys(t *testing.T) {
	model := Model{snapshot: sampleSnapshot(), width: 140, height: 40, pickingAgent: true}

	// Keys with meaning in the main view (e.g. tab focus-cycling) must not
	// leak through while the picker overlay is open.
	updated, _ := model.Update(specialKey(tea.KeyTab))
	next := updated.(Model)
	if next.focus != focusSessions {
		t.Fatalf("expected focus to be unaffected while picker is open, got %v", next.focus)
	}
	if !next.pickingAgent {
		t.Fatal("expected picker to remain open for an unhandled key")
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
