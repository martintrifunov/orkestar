package tui

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/martintrifunov/orkestar/internal/daemon"
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

// An attachment or mutation reply that lands after a switch belongs to the
// old machine: installing it would put A's stream into B's layout or report
// A's outcome as B's.
func TestStaleAsyncRepliesAreDroppedAfterSwitch(t *testing.T) {
	m := New(ipc.NewClient("local"), t.TempDir())
	m.machines = []Machine{
		{ID: "local", Label: "Local", Client: ipc.NewClient("a")},
		{ID: "m1", Label: "Build", Client: ipc.NewClient("b")},
	}
	m.machineIndex = 1
	stalePane := fakePane(t, "stale")

	updated, _ := m.Update(embeddedReadyMsg{terminal: stalePane, machineID: "local"})
	m = updated.(Model)
	if m.tree() != nil {
		stalePane.close()
		t.Fatal("an attachment from the previous machine was installed")
	}

	updated, _ = m.Update(taskActionMsg{notice: "Task done", machineID: "local"})
	m = updated.(Model)
	if m.notice == "Task done" || m.taskBusy {
		t.Fatal("a task reply from the previous machine was applied")
	}

	updated, _ = m.Update(lifecycleMsg{notice: "Stopped x", machineID: "local"})
	m = updated.(Model)
	if m.notice == "Stopped x" {
		t.Fatal("a lifecycle reply from the previous machine was applied")
	}

	updated, _ = m.Update(historyMsg{lines: []string{"old"}, machineID: "local"})
	m = updated.(Model)
	if m.viewingHistory {
		t.Fatal("history from the previous machine was shown")
	}
}

// Switching must not carry a confirm, a busy flag or a half-typed prompt to
// the new machine, where it would act on the wrong board.
func TestSwitchMachineClearsInteractionState(t *testing.T) {
	m := New(ipc.NewClient("local"), t.TempDir())
	m.machines = []Machine{
		{ID: "local", Label: "Local", Client: ipc.NewClient("a")},
		{ID: "m1", Label: "Build", Client: ipc.NewClient("b")},
	}
	m.pendingStop = "term_abc"
	m.taskBusy = true
	m.taskPrompt = true
	m.taskTitle = "draft"
	m.filePrompt = true
	m.renaming = fakePane(t, "r")
	m.menu = &paneMenu{}

	_ = m.switchMachine(1)
	if m.pendingStop != "" || m.taskBusy || m.taskPrompt || m.taskTitle != "" || m.filePrompt || m.renaming != nil || m.menu != nil {
		t.Fatal("interaction state survived a machine switch")
	}
}

// On a remote machine the local checkout means nothing: reusing what that
// machine already has beats rooting a workspace at a local-only path.
func TestEnsureWorkspaceOnRemoteReusesExisting(t *testing.T) {
	remote := ipc.NewRemoteClient(ipc.Remote{Host: "example.com"})
	m := New(remote, "/local/checkout")
	m.machines = []Machine{{ID: "r", Label: "Remote", Client: remote}}
	m.client = remote
	m.snapshot.Workspaces = []daemon.Workspace{{ID: "w-remote", Directory: "/home/user/work"}}

	id, err := m.ensureWorkspace(context.Background())
	if err != nil {
		t.Fatalf("remote with a workspace should not fail: %v", err)
	}
	if id != "w-remote" {
		t.Fatalf("expected the remote workspace, got %q", id)
	}

	m.snapshot.Workspaces = nil
	if _, err := m.ensureWorkspace(context.Background()); err == nil {
		t.Fatal("remote with no workspace should fail clearly, not create a local path remotely")
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
