package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/martintrifunov/orkestar/internal/daemon"
)

// A saved layout keeps what a pane was, not just a terminal ID that means
// nothing on another daemon.
func TestPortableLayoutRoundTrip(t *testing.T) {
	m := Model{width: 160, height: 42}
	m.addPane(fakePane(t, "term_1"))
	m.insertPane(fakePane(t, "term_2"), m.embedded, false)
	m.snapshot.Terminals = []daemon.Terminal{
		{ID: "term_1", Command: []string{"nvim"}, Directory: "/work", State: "running"},
		{ID: "term_2", Command: []string{"go", "test", "./..."}, Directory: "/work", State: "running"},
	}
	m.embedded.title = "editor"
	directory := t.TempDir()
	m.layoutsDir = directory

	if result := m.saveCurrentLayout("dev")(); result.(layoutActionMsg).err != nil {
		t.Fatalf("save failed: %v", result.(layoutActionMsg).err)
	}
	saved, err := readPortableLayout(filepath.Join(directory, "dev.json"))
	if err != nil {
		t.Fatal(err)
	}
	if countPortablePanes(saved.Root) != 2 {
		t.Fatalf("wrong pane count: %+v", saved.Root)
	}
	leaves := []*portablePane{}
	var walk func(*portableNode)
	walk = func(node *portableNode) {
		if node == nil {
			return
		}
		if node.Pane != nil {
			leaves = append(leaves, node.Pane)
			return
		}
		walk(node.First)
		walk(node.Second)
	}
	walk(saved.Root)
	if len(leaves) != 2 || leaves[0].Terminal != "term_1" || leaves[0].Command[0] != "nvim" {
		t.Fatalf("first pane did not round-trip: %+v", leaves)
	}
	if leaves[1].Terminal != "term_2" || leaves[1].Directory != "/work" {
		t.Fatalf("second pane did not round-trip: %+v", leaves[1])
	}

	// The summary lists what the overlay shows.
	summaries, err := listLayoutFiles(directory)
	if err != nil || len(summaries) != 1 || summaries[0].Name != "dev" || summaries[0].Panes != 2 {
		t.Fatalf("listing is wrong: %v %+v", err, summaries)
	}
}

// Applying reuses terminals that are still running, by ID first and command
// second, without starting anything new.
func TestApplyPortableLayoutReusesRunningTerminals(t *testing.T) {
	client := startEmbeddedTestDaemon(t)
	first := startEmbeddedTestTerminal(t, client, []string{"/bin/sh"})
	second := startEmbeddedTestTerminal(t, client, []string{"/bin/sh"})

	saved := portableLayout{Version: 1, Focus: 1, Root: &portableNode{
		First:  &portableNode{Pane: &portablePane{Terminal: first.ID, Command: []string{"/bin/sh"}}},
		Second: &portableNode{Pane: &portablePane{Terminal: "term_gone", Command: []string{"/bin/sh"}}},
	}}
	msg := applyPortableLayout(context.Background(), client, "local", first.WorkspaceID, saved,
		[]daemon.Terminal{first, second}, 80, 24)
	restored, ok := msg.(layoutRestoredMsg)
	if !ok {
		t.Fatalf("layout did not apply: %#v", msg)
	}
	defer func() {
		for _, pane := range restored.panes {
			pane.close()
		}
	}()
	if len(restored.panes) != 2 || restored.tree == nil {
		t.Fatalf("expected two panes, got %+v", restored.panes)
	}
	got := leafIDs(Model{layout: restored.tree})
	if len(got) != 2 || got[0] != first.ID || got[1] != second.ID {
		t.Fatalf("the missing ID was not matched by command: %v", got)
	}
	if restored.focus == nil || restored.focus.terminalID != second.ID {
		t.Fatalf("the saved focus was not kept: %+v", restored.focus)
	}
}

// A leaf with nothing running to match starts the command it saved.
func TestApplyPortableLayoutStartsMissingPanes(t *testing.T) {
	client := startEmbeddedTestDaemon(t)
	seed := startEmbeddedTestTerminal(t, client, []string{"/bin/sh"})

	saved := portableLayout{Version: 1, Focus: 0, Root: &portableNode{
		Pane: &portablePane{Command: []string{"/bin/sh"}, Directory: seed.Directory},
	}}
	msg := applyPortableLayout(context.Background(), client, "local", seed.WorkspaceID, saved, nil, 80, 24)
	restored, ok := msg.(layoutRestoredMsg)
	if !ok {
		t.Fatalf("layout did not apply: %#v", msg)
	}
	defer func() {
		for _, pane := range restored.panes {
			pane.close()
		}
	}()
	if len(restored.panes) != 1 || restored.tree == nil {
		t.Fatalf("the pane was not started: %+v", restored.panes)
	}
}

// The overlay saves, lists and deletes through the keyboard.
func TestLayoutsOverlayFlow(t *testing.T) {
	directory := t.TempDir()
	m := Model{width: 160, height: 42, layoutsDir: directory}
	m.addPane(fakePane(t, "term_1"))
	m.snapshot.Terminals = []daemon.Terminal{{ID: "term_1", Command: []string{"sh"}, State: "running"}}

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	m = updated.(Model)
	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'l'})
	m = updated.(Model)
	if !m.layoutsOpen || cmd == nil {
		t.Fatal("ctrl+b l did not open the layouts overlay")
	}
	m = settle(t, m, cmd)
	if !strings.Contains(m.layoutsView(), "No saved layouts") {
		t.Fatalf("empty overlay does not say so:\n%s", m.layoutsView())
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: 's'})
	m = updated.(Model)
	if !m.namingLayout {
		t.Fatal("s did not open the save prompt")
	}
	m = typeText(t, m, "work")
	updated, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	m = settle(t, m, cmd)
	if m.err != nil || m.layoutsErr != nil {
		t.Fatalf("save failed: %v / %v", m.err, m.layoutsErr)
	}
	if !strings.Contains(m.layoutsView(), "work") || !strings.Contains(m.notice, "saved") {
		t.Fatalf("the saved layout is not listed: %q\n%s", m.notice, m.layoutsView())
	}
	if _, err := os.Stat(filepath.Join(directory, "work.json")); err != nil {
		t.Fatalf("no file was written: %v", err)
	}

	updated, cmd = m.Update(tea.KeyPressMsg{Code: 'x'})
	m = updated.(Model)
	m = settle(t, m, cmd)
	if _, err := os.Stat(filepath.Join(directory, "work.json")); !os.IsNotExist(err) {
		t.Fatalf("delete left the file: %v", err)
	}
}
