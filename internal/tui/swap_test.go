package tui

import "testing"

func TestSwapLeavesExchangesPanes(t *testing.T) {
	first := terminalLeaf("term_1").pane
	second := terminalLeaf("term_2").pane
	tree := &splitNode{ratio: 0.5, first: &splitNode{pane: first}, second: &splitNode{pane: second}}

	if !swapLeaves(tree, first, second) {
		t.Fatal("expected the swap to find both panes")
	}
	leaves := tree.leaves(nil)
	if len(leaves) != 2 || leaves[0] != second || leaves[1] != first {
		t.Fatalf("panes were not exchanged: %#v", leaves)
	}
	if tree.ratio != 0.5 {
		t.Fatalf("the split ratio changed: %v", tree.ratio)
	}

	if swapLeaves(tree, first, terminalLeaf("term_missing").pane) {
		t.Fatal("a swap with a pane that is not in the tree should not report success")
	}
}

func TestWindowTitleNamesTheFocusedPane(t *testing.T) {
	pane := terminalLeaf("term_1").pane
	if title := (Model{embedded: pane}).windowTitle(); title != "Orkestar · term_1" {
		t.Fatalf("unexpected title: %q", title)
	}
	if title := (Model{embedded: pane, sidebarFocused: true}).windowTitle(); title != "Orkestar" {
		t.Fatalf("a focused sidebar should leave the plain title: %q", title)
	}
	if title := (Model{}).windowTitle(); title != "Orkestar" {
		t.Fatalf("no pane should leave the plain title: %q", title)
	}
}
