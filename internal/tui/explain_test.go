package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/martintrifunov/orkestar/internal/agent"
	"github.com/martintrifunov/orkestar/internal/daemon"
)

// agent.explain answers why Orkestar believes an agent is in its state, but
// only the CLI and MCP could ask; the sidebar that shows the state had no way
// to ask what is behind it.
func TestExplainShowsWhyAnAgentIsInItsState(t *testing.T) {
	root := taskRepo(t)
	adapter := agent.NewFakeAdapter(agent.Capabilities{
		Name: "fake", SupportsInteractive: true, SupportsPrompt: true,
	})
	client := startEmbeddedTestDaemon(t, adapter)
	ctx := context.Background()
	var workspace daemon.Workspace
	if err := client.Call(ctx, "workspace.create", map[string]string{"directory": root}, &workspace); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	var launched daemon.Agent
	if err := client.Call(ctx, "agent.launch", map[string]any{
		"workspace_id": workspace.ID, "adapter": "fake", "mode": "interactive",
	}, &launched); err != nil {
		t.Fatalf("launch agent: %v", err)
	}

	m := New(client, root)
	m.width, m.height = 160, 44
	m.focus = focusAgents
	m.snapshot.Agents = []daemon.Agent{launched}
	m.agentSelected = 0

	m, cmd := press(t, m, 'E')
	if !m.viewingExplanation || cmd == nil {
		t.Fatal("E did not ask for an explanation")
	}
	if view := m.renderExplanation(100); !strings.Contains(view, "Reading") {
		t.Fatalf("the overlay does not show that it is waiting:\n%s", view)
	}
	m = settle(t, m, cmd)
	if m.explainErr != nil {
		t.Fatalf("explain failed: %v", m.explainErr)
	}
	if !m.explanation.Live || m.explanation.Resumable {
		t.Fatalf("liveness is wrong: %+v", m.explanation)
	}
	view := m.renderExplanation(100)
	for _, want := range []string{"fake", "live process: yes", "resumable: no", launched.ID} {
		if !strings.Contains(view, want) {
			t.Fatalf("explanation does not mention %q:\n%s", want, view)
		}
	}
	if !strings.Contains(m.helpLine(), "esc close explanation") {
		t.Fatalf("the help line does not say how to leave: %q", m.helpLine())
	}

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(Model)
	if m.viewingExplanation {
		t.Fatal("esc did not close the explanation")
	}
}
