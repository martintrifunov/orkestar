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

// A poll that lands after the board emptied must not leave the agent cursor
// out of range: several key handlers index it. Regression for a -1 index.
func TestRemotesMsgClampsTheAgentSelection(t *testing.T) {
	m := New(ipc.NewClient("local"), t.TempDir())
	m.agentSelected = 3
	m.snapshot = daemon.Snapshot{}

	updated, _ := m.Update(remotesMsg{views: []MachineView{{ID: "m1", Label: "Build", State: "online"}}})
	m = updated.(Model)
	if m.agentSelected != 0 {
		t.Fatalf("expected the cursor to clamp to 0, got %d", m.agentSelected)
	}
	if _, ok := m.lifecycleTarget(); ok {
		t.Fatal("there is no agent to target")
	}
	_ = m.resumeSelected()
	_ = m.resolveSelectedPermission("allow")
}

// A poll taken for a previous selection names the wrong machines, so it is
// dropped; the in-flight guard must still clear so polling resumes.
func TestStaleRemotesMsgIsDropped(t *testing.T) {
	m := New(ipc.NewClient("local"), t.TempDir())
	m.machineIndex = 1
	m.polling = true

	updated, _ := m.Update(remotesMsg{machineIndex: 0, views: []MachineView{{ID: "x", Label: "Old"}}})
	m = updated.(Model)
	if m.remote != nil {
		t.Fatalf("a poll for another selection was applied: %#v", m.remote)
	}
	if m.polling {
		t.Fatal("polling should clear even when the poll is stale")
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
