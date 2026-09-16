package tui

import (
	"strings"
	"testing"

	"github.com/martintrifunov/orkestar/internal/daemon"
	"github.com/martintrifunov/orkestar/internal/ipc"
)

func TestRenderAgentsShowsMergedMachines(t *testing.T) {
	local := ipc.NewClient("local")
	remote := ipc.NewClient("remote")
	m := New(local, t.TempDir())
	m.machines = []Machine{
		{ID: "local", Label: "Local", Client: local},
		{ID: "m1", Label: "Build", Client: remote},
	}
	m.snapshot.Agents = []daemon.Agent{{ID: "a1", Adapter: "claude-code", State: "working"}}
	m.remote = []MachineView{{
		ID: "m1", Label: "Build", State: "online",
		Snapshot: daemon.Snapshot{Agents: []daemon.Agent{{ID: "a2", Adapter: "codex", State: "working"}}},
	}}

	rendered := m.renderAgents()
	if !strings.Contains(rendered, "claude-code") || !strings.Contains(rendered, "[Local]") {
		t.Fatalf("the local agent row lost its machine column: %q", rendered)
	}
	if !strings.Contains(rendered, "codex") || !strings.Contains(rendered, "[Build]") {
		t.Fatalf("the remote agent row lost its machine column: %q", rendered)
	}
	if !strings.Contains(rendered, "Other machines") || !strings.Contains(rendered, "Build  online") {
		t.Fatalf("the machine status is missing: %q", rendered)
	}
}

func TestRenderAgentsSingleMachineHasNoColumn(t *testing.T) {
	m := New(ipc.NewClient("solo"), t.TempDir())
	m.snapshot.Agents = []daemon.Agent{{ID: "a1", Adapter: "claude-code", State: "working"}}

	rendered := m.renderAgents()
	if strings.Contains(rendered, "  [") {
		t.Fatalf("a single machine should not show a machine column: %q", rendered)
	}
}
