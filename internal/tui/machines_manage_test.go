package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/martintrifunov/orkestar/internal/ipc"
	"github.com/martintrifunov/orkestar/internal/machine"
)

// Machine profiles could only be edited with `orkestar machine ...`, so the
// interface that switches between machines could not add or remove one.
func TestManageMachinesFromSettings(t *testing.T) {
	root := t.TempDir()
	catalogPath := filepath.Join(root, "machines.json")
	layoutRoot := filepath.Join(root, "runtime")
	client := startEmbeddedTestDaemon(t)
	m := New(client, root)
	m.width, m.height = 160, 44
	m.machineCatalogPath = catalogPath
	m.machineLayoutRoot = layoutRoot
	m.settingsOpen = true

	m, cmd := press(t, m, 'm')
	if !m.managingMachines || cmd == nil {
		t.Fatal("m did not open the machine overlay")
	}
	m = settle(t, m, cmd)
	if m.machinesErr != nil {
		t.Fatalf("listing machines: %v", m.machinesErr)
	}
	if !strings.Contains(m.promptView(), "No saved machines") {
		t.Fatalf("the empty overlay does not say so:\n%s", m.promptView())
	}

	// Add, with a label that differs from the host.
	m, _ = press(t, m, 'a')
	if !m.addingMachine {
		t.Fatal("a did not open the add form")
	}
	m = typeText(t, m, "build.example")
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	m = updated.(Model)
	m = typeText(t, m, "Build")
	updated, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if cmd == nil || m.addingMachine {
		t.Fatal("enter did not submit the form")
	}
	m = settle(t, m, cmd)
	if m.machinesErr != nil {
		t.Fatalf("add failed: %v", m.machinesErr)
	}
	if len(m.savedMachines) != 1 || m.savedMachines[0].Label != "Build" || m.savedMachines[0].Host != "build.example" {
		t.Fatalf("the profile is wrong: %+v", m.savedMachines)
	}
	if len(m.machines) != 2 {
		t.Fatalf("the machine was not added to the session: %+v", m.machines)
	}
	catalog, err := machine.Load(catalogPath)
	if err != nil || len(catalog.List()) != 1 {
		t.Fatalf("the catalog was not saved: %v %+v", err, catalog)
	}

	// Disable it: the profile stays, the session stops watching it.
	m, cmd = press(t, m, 'e')
	m = settle(t, m, cmd)
	if m.machinesErr != nil {
		t.Fatalf("disable failed: %v", m.machinesErr)
	}
	if m.savedMachines[0].Enabled || len(m.machines) != 1 {
		t.Fatalf("disable did not detach: enabled=%v machines=%d", m.savedMachines[0].Enabled, len(m.machines))
	}
	catalog, _ = machine.Load(catalogPath)
	if catalog.List()[0].Enabled {
		t.Fatal("disable was not persisted")
	}

	// Enable it again.
	m, cmd = press(t, m, 'e')
	m = settle(t, m, cmd)
	if m.machinesErr != nil {
		t.Fatalf("enable failed: %v", m.machinesErr)
	}
	if !m.savedMachines[0].Enabled || len(m.machines) != 2 {
		t.Fatalf("enable did not attach: enabled=%v machines=%d", m.savedMachines[0].Enabled, len(m.machines))
	}

	// Remove it: profile gone from disk and session, layout directory gone.
	if err := os.MkdirAll(filepath.Join(layoutRoot, "machines", m.savedMachines[0].ID), 0755); err != nil {
		t.Fatal(err)
	}
	m, cmd = press(t, m, 'x')
	m = settle(t, m, cmd)
	if m.machinesErr != nil {
		t.Fatalf("remove failed: %v", m.machinesErr)
	}
	if len(m.savedMachines) != 0 || len(m.machines) != 1 {
		t.Fatalf("remove did not take: saved=%d session=%d", len(m.savedMachines), len(m.machines))
	}
	if _, err := os.Stat(filepath.Join(layoutRoot, "machines", catalog.List()[0].ID)); !os.IsNotExist(err) {
		t.Fatalf("the layout directory survived removal: %v", err)
	}
	catalog, _ = machine.Load(catalogPath)
	if len(catalog.List()) != 0 {
		t.Fatalf("the catalog still holds machines: %+v", catalog.List())
	}
}

// Removing the machine on screen has to bring the local board back rather
// than leaving the selection pointing at a machine that no longer exists.
func TestRemovingTheSelectedMachineReturnsToLocal(t *testing.T) {
	root := t.TempDir()
	catalogPath := filepath.Join(root, "machines.json")
	catalog, err := machine.Load(catalogPath)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := catalog.Add("Build", "build.example", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := catalog.Save(); err != nil {
		t.Fatal(err)
	}

	local := startEmbeddedTestDaemon(t)
	m := New(local, root)
	m.width, m.height = 160, 44
	m.machineCatalogPath = catalogPath
	m.machineLayoutRoot = filepath.Join(root, "runtime")
	remoteClient := ipc.NewClient("ssh build.example")
	m.machines = append(m.machines, Machine{
		ID: saved.ID, Label: saved.Label, Client: remoteClient, LayoutPath: m.machineLayoutPath(saved.ID),
	})
	m.machineIndex = 1
	m.client = remoteClient
	m.settingsOpen = true

	m, cmd := press(t, m, 'm')
	m = settle(t, m, cmd)
	m, cmd = press(t, m, 'x')
	m = settle(t, m, cmd)
	if m.machinesErr != nil {
		t.Fatalf("remove failed: %v", m.machinesErr)
	}
	if m.machineIndex != 0 || len(m.machines) != 1 {
		t.Fatalf("selection did not return to local: index=%d machines=%+v", m.machineIndex, m.machines)
	}
	if m.client != local {
		t.Fatal("the client did not return to the local daemon")
	}
}

// A --remote interface has no local catalog to manage.
func TestMachineManagementIsUnavailableOnRemote(t *testing.T) {
	m := New(ipc.NewRemoteClient(ipc.Remote{Host: "example.com"}), t.TempDir())
	if cmd := m.openMachines(); cmd != nil || m.managingMachines {
		t.Fatal("machine management opened in a remote session")
	}
	if !strings.Contains(m.notice, "unavailable") {
		t.Fatalf("no reason was given: %q", m.notice)
	}
}
