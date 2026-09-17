package tui

import (
	"context"
	"path/filepath"
	"testing"
)

func terminalLeaf(id string) *splitNode {
	return &splitNode{pane: &embeddedTerminal{terminalID: id}}
}

func TestPersistedTreeDropsNonTerminalPanes(t *testing.T) {
	editor := &splitNode{pane: &embeddedTerminal{editor: &textEditor{}}}
	tree := &splitNode{stacked: true, ratio: 0.4, first: terminalLeaf("term_1"), second: editor}

	persisted := persistedTree(tree)
	if persisted == nil || persisted.Terminal != "term_1" {
		t.Fatalf("expected the editor pane to be dropped, got %#v", persisted)
	}
}

func TestRebuiltTreeDropsMissingTerminals(t *testing.T) {
	persisted := &persistedNode{
		Stacked: true,
		First:   &persistedNode{Terminal: "term_1"},
		Second:  &persistedNode{Terminal: "term_gone"},
	}
	tree := rebuiltTree(persisted, map[string]*embeddedTerminal{"term_1": terminalLeaf("term_1").pane})
	if tree == nil || tree.pane == nil || tree.pane.terminalID != "term_1" {
		t.Fatalf("expected the split to collapse to the surviving pane, got %#v", tree)
	}
	if tree := rebuiltTree(persisted, map[string]*embeddedTerminal{}); tree != nil {
		t.Fatalf("expected no tree when nothing survived, got %#v", tree)
	}
}

func TestSaveLoadLayoutRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "layout.json")
	tree := &splitNode{ratio: 0.3, first: terminalLeaf("term_1"), second: terminalLeaf("term_2")}
	saveLayout(path, tree, "term_2")

	saved, ok := loadLayout(path)
	if !ok || saved.Focus != "term_2" {
		t.Fatalf("layout did not round-trip: %#v %v", saved, ok)
	}
	rebuilt := rebuiltTree(saved.Root, map[string]*embeddedTerminal{
		"term_1": terminalLeaf("term_1").pane,
		"term_2": terminalLeaf("term_2").pane,
	})
	if rebuilt == nil || rebuilt.first == nil || rebuilt.second == nil || rebuilt.first.pane.terminalID != "term_1" {
		t.Fatalf("unexpected rebuilt tree: %#v", rebuilt)
	}
	if rebuilt.ratio != 0.3 {
		t.Fatalf("ratio not preserved: %v", rebuilt.ratio)
	}

	// Saving an empty tree removes the file rather than leaving it empty.
	saveLayout(path, nil, "")
	if _, ok := loadLayout(path); ok {
		t.Fatal("expected the layout file to be removed")
	}
	if _, ok := loadLayout(filepath.Join(t.TempDir(), "absent.json")); ok {
		t.Fatal("expected a missing layout file to report false")
	}
}

func TestPersistLayoutWritesTheModel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "layout.json")
	leaf := terminalLeaf("term_9").pane
	model := Model{layoutPath: path, layout: &splitNode{pane: leaf}, embedded: leaf}
	model.persistLayout()

	saved, ok := loadLayout(path)
	if !ok || saved.Focus != "term_9" || saved.Root == nil || saved.Root.Terminal != "term_9" {
		t.Fatalf("persistLayout did not write the model layout: %#v %v", saved, ok)
	}

	// No path means no persistence, and no panic.
	Model{layout: &splitNode{pane: leaf}}.persistLayout()
}

// A saved structure must not restore the same terminal twice: two leaves would
// share one attachment, and closing one would leave the other dangling.
func TestRestoreKeepsADuplicatedTerminalOnce(t *testing.T) {
	persisted := &persistedNode{
		Stacked: true,
		First:   &persistedNode{Terminal: "term_1"},
		Second:  &persistedNode{Terminal: "term_1"},
	}
	panes := map[string]*embeddedTerminal{"term_1": terminalLeaf("term_1").pane}
	tree := rebuiltTree(persisted, panes)
	if tree == nil || tree.pane == nil || tree.pane.terminalID != "term_1" {
		t.Fatalf("expected a single surviving pane, got %#v", tree)
	}
	if leaves := tree.leaves(nil); len(leaves) != 1 {
		t.Fatalf("the terminal was restored %d times", len(leaves))
	}
}

// A saved layout is restored against the terminals a real daemon is still
// running: alive ones come back, dead ones are dropped, and a split with no
// live child collapses away.
func TestRestoreLayoutAttachesLiveTerminals(t *testing.T) {
	client := startEmbeddedTestDaemon(t)
	first := startEmbeddedTestTerminal(t, client, []string{"/bin/sh", "-c", "printf 'one\\n'; while :; do sleep 1; done"})
	second := startEmbeddedTestTerminal(t, client, []string{"/bin/sh", "-c", "printf 'two\\n'; while :; do sleep 1; done"})

	path := filepath.Join(t.TempDir(), "layout.json")
	tree := &splitNode{ratio: 0.5, first: terminalLeaf(first.ID), second: terminalLeaf(second.ID)}
	saveLayout(path, tree, second.ID)
	saved, ok := loadLayout(path)
	if !ok {
		t.Fatal("saved layout did not load")
	}

	restored := restoreLayout(context.Background(), client, "local", saved, map[string]bool{first.ID: true, second.ID: true})().(layoutRestoredMsg)
	if restored.tree == nil || len(restored.panes) != 2 {
		t.Fatalf("expected both live terminals to come back, got %#v", restored)
	}
	if restored.focus == nil || restored.focus.terminalID != second.ID {
		t.Fatalf("expected focus to return to the saved pane, got %#v", restored.focus)
	}
	for _, pane := range restored.panes {
		pane.close()
	}

	collapsed := restoreLayout(context.Background(), client, "local", saved, map[string]bool{first.ID: true})().(layoutRestoredMsg)
	if collapsed.tree == nil || collapsed.tree.pane == nil || collapsed.tree.pane.terminalID != first.ID {
		t.Fatalf("expected the tree to collapse to the running pane, got %#v", collapsed.tree)
	}
	collapsed.tree.pane.close()
}
