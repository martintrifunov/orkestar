package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func moveModel(t *testing.T) Model {
	t.Helper()
	m := Model{width: 160, height: 42}
	m.addPane(fakePane(t, "0"))
	m.insertPane(fakePane(t, "1"), m.embedded, false) // 0 | 1
	m.insertPane(fakePane(t, "2"), m.embedded, true)  // 0 | (1/2)
	return m
}

func leafIDs(m Model) []string {
	var ids []string
	for _, pane := range m.visiblePanes() {
		ids = append(ids, pane.terminalID)
	}
	return ids
}

// Moving a pane on top of a simple pair puts it on the far side, which is
// what moving toward the neighbour means.
func TestMovePaneBesideItsNeighbour(t *testing.T) {
	m := Model{width: 160, height: 42}
	m.addPane(fakePane(t, "0"))
	m.insertPane(fakePane(t, "1"), m.embedded, false)
	m.embedded = m.visiblePanes()[0]
	m.zoomed = false
	m.movePaneInDirection("right")
	if got := leafIDs(m); len(got) != 2 || got[0] != "1" || got[1] != "0" {
		t.Fatalf("pane 0 did not move right: %v", got)
	}
	if m.embedded.terminalID != "0" {
		t.Fatal("the moved pane lost focus")
	}
	assertTiled(t, m, 2)
}

// In a nested layout the move changes the shape: the pane leaves its own
// split, and the target's split grows a new side.
func TestMovePaneReparentsInTheTree(t *testing.T) {
	m := moveModel(t)
	m.embedded = m.visiblePanes()[0] // pane 0, beside the 1/2 column
	m.movePaneInDirection("right")
	if got := leafIDs(m); len(got) != 3 || got[0] != "1" || got[1] != "0" || got[2] != "2" {
		t.Fatalf("unexpected layout after moving right: %v", got)
	}
	assertTiled(t, m, 3)

	m.embedded = m.visiblePanes()[2] // pane 2, under pane 1
	m.movePaneInDirection("up")
	if got := leafIDs(m); len(got) != 3 || got[0] != "2" || got[1] != "1" || got[2] != "0" {
		t.Fatalf("unexpected layout after moving up: %v", got)
	}
	assertTiled(t, m, 3)
}

// An arrow with nothing in that direction says so instead of rearranging
// something the user did not point at.
func TestMovePaneWithoutATarget(t *testing.T) {
	m := moveModel(t)
	m.embedded = m.visiblePanes()[0]
	m.movePaneInDirection("down")
	if !strings.Contains(m.notice, "No pane to the down") {
		t.Fatalf("no reason was given: %q", m.notice)
	}
	if got := leafIDs(m); len(got) != 3 || got[0] != "0" {
		t.Fatalf("the layout changed anyway: %v", got)
	}
}

// Ctrl+b m arms the mode; the next arrow moves and any other key cancels
// without reaching the terminal.
func TestMovePaneKeyFlow(t *testing.T) {
	m := moveModel(t)
	m.embedded = m.visiblePanes()[0]

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'm'})
	m = updated.(Model)
	if !m.movingPane {
		t.Fatal("ctrl+b m did not arm the move")
	}
	if !strings.Contains(m.helpLine(), "move pane") {
		t.Fatalf("the help line does not describe the mode: %q", m.helpLine())
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	m = updated.(Model)
	if m.movingPane {
		t.Fatal("the move did not finish")
	}
	if got := leafIDs(m); got[1] != "0" {
		t.Fatalf("the arrow did not move the pane: %v", got)
	}

	// Arming again and pressing something else cancels quietly.
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	m = updated.(Model)
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'm'})
	m = updated.(Model)
	before := leafIDs(m)
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'z'})
	m = updated.(Model)
	if m.movingPane {
		t.Fatal("another key did not cancel the move")
	}
	if got := leafIDs(m); strings.Join(got, ",") != strings.Join(before, ",") {
		t.Fatalf("a cancelled move changed the layout: %v", got)
	}
}
