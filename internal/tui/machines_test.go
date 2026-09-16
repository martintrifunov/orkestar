package tui

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/martintrifunov/orkestar/internal/ipc"
)

func TestSwitchMachineRepointsTheClientAndLayout(t *testing.T) {
	firstLayout := filepath.Join(t.TempDir(), "local.json")
	secondLayout := filepath.Join(t.TempDir(), "build.json")
	first := ipc.NewClient("first")
	second := ipc.NewClient("second")

	m := New(first, t.TempDir())
	m.machines = []Machine{
		{ID: "local", Label: "Local", Client: first, LayoutPath: firstLayout},
		{ID: "m1", Label: "Build", Client: second, LayoutPath: secondLayout},
	}
	m.layoutPath = firstLayout

	cmd := m.switchMachine(1)
	if cmd == nil {
		t.Fatal("switching should reload the snapshot")
	}
	if m.machineIndex != 1 {
		t.Fatalf("expected the second machine, got index %d", m.machineIndex)
	}
	if m.client != second {
		t.Fatal("the client did not follow the selection")
	}
	if m.layoutPath != secondLayout {
		t.Fatalf("the layout did not follow the selection: %q", m.layoutPath)
	}
	if !m.loading || m.layoutRestored {
		t.Fatal("switching should reload and re-allow a layout restore")
	}
	if !strings.Contains(m.notice, "Build") {
		t.Fatalf("the switch was not announced: %q", m.notice)
	}
	if m.currentMachine().Label != "Build" {
		t.Fatalf("unexpected current machine: %#v", m.currentMachine())
	}
}

func TestSwitchMachineWithOnlyOne(t *testing.T) {
	m := New(ipc.NewClient("solo"), t.TempDir())
	if cmd := m.switchMachine(1); cmd != nil {
		t.Fatal("switching with a single machine should do nothing")
	}
	if !strings.Contains(m.notice, "No other machines") {
		t.Fatalf("expected a hint to add a machine, got %q", m.notice)
	}
}
