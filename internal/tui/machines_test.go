package tui

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/martintrifunov/orkestar/internal/files"
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

func TestSwitchMachineRefusesWhenAnEditorIsDirty(t *testing.T) {
	m := New(ipc.NewClient("local"), t.TempDir())
	m.machines = []Machine{
		{ID: "local", Label: "Local", Client: ipc.NewClient("a")},
		{ID: "m1", Label: "Build", Client: ipc.NewClient("b")},
	}
	editor := newTextEditor(&files.Document{Path: "notes.txt", Text: "saved"}, resolveTheme(""))
	editor.text = []rune("changed")
	pane := &embeddedTerminal{editor: editor}
	m.layout = &splitNode{pane: pane}
	m.embedded = pane

	if cmd := m.switchMachine(1); cmd != nil {
		t.Fatal("switching machines with a dirty editor should be refused")
	}
	if m.machineIndex != 0 {
		t.Fatal("the machine changed despite a dirty editor")
	}
	if !strings.Contains(m.notice, "Unsaved") {
		t.Fatalf("no warning was shown: %q", m.notice)
	}
}

func TestSwitchMachineClosesTheLocalFileViewer(t *testing.T) {
	m := New(ipc.NewClient("local"), t.TempDir())
	m.machines = []Machine{
		{ID: "local", Label: "Local", Client: ipc.NewClient("a")},
		{ID: "m1", Label: "Build", Client: ipc.NewClient("b")},
	}
	m.filesOpen = true
	m.filesFocused = true
	m.filesRoot = t.TempDir()

	_ = m.switchMachine(1)
	if m.filesOpen || m.filesFocused {
		t.Fatal("the local file viewer survived a machine switch")
	}
}

func TestStaleLayoutRestoreIsDropped(t *testing.T) {
	m := New(ipc.NewClient("local"), t.TempDir())
	m.machines = []Machine{
		{ID: "local", Label: "Local", Client: ipc.NewClient("a")},
		{ID: "m1", Label: "Build", Client: ipc.NewClient("b")},
	}
	m.machineIndex = 1
	stale := fakePane(t, "old")

	updated, _ := m.Update(layoutRestoredMsg{
		machineID: "local", tree: &splitNode{pane: stale}, focus: stale,
		panes: []*embeddedTerminal{stale},
	})
	m = updated.(Model)
	if m.layout != nil || m.embedded != nil {
		t.Fatal("a layout restore for another machine was installed")
	}
}

func TestLayoutRestoreDoesNotReplaceAnOpenPane(t *testing.T) {
	m := New(ipc.NewClient("local"), t.TempDir())
	open := fakePane(t, "mine")
	m.layout = &splitNode{pane: open}
	m.embedded = open
	restored := fakePane(t, "restored")

	updated, _ := m.Update(layoutRestoredMsg{
		machineID: m.currentMachine().ID, tree: &splitNode{pane: restored}, focus: restored,
		panes: []*embeddedTerminal{restored},
	})
	m = updated.(Model)
	if m.embedded != open {
		t.Fatal("a layout restore replaced an already-open pane")
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
