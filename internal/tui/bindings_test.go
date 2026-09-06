package tui

import (
	"strings"
	"testing"
)

// Anything a user does not rebind must keep working exactly as before.
func TestDefaultBindingsAreUnchanged(t *testing.T) {
	keys, complaints := newBindings(nil)
	if len(complaints) != 0 {
		t.Fatalf("the defaults complain about themselves: %v", complaints)
	}
	for action, key := range map[Action]string{
		ActionPrefix: "ctrl+b", ActionQuit: "q", ActionNewTask: "c",
		ActionTaskDone: "m", ActionStopRemove: "X", ActionSection: "tab",
	} {
		if got := keys.key(action); got != key {
			t.Errorf("%s is bound to %q, want %q", action, got, key)
		}
	}
	if keys.action("c") != ActionNewTask {
		t.Fatal("c does not create a task")
	}
	// The same letter means different things with and without the prefix,
	// which is what a prefix is for.
	if keys.action("v") == ActionSplitRight {
		t.Fatal("v splits without the prefix")
	}
	if keys.prefixAction("v") != ActionSplitRight {
		t.Fatal("the prefix does not reach a split")
	}
}

// The point of the exercise: someone arriving with different muscle memory
// changes the map rather than their fingers.
func TestBindingsCanBeOverridden(t *testing.T) {
	keys, complaints := newBindings(map[string]string{
		"prefix":   "ctrl+a",
		"new-task": "N",
	})
	if len(complaints) != 0 {
		t.Fatalf("valid overrides complained: %v", complaints)
	}
	if keys.key(ActionPrefix) != "ctrl+a" {
		t.Fatalf("the prefix is %q", keys.key(ActionPrefix))
	}
	if keys.action("N") != ActionNewTask {
		t.Fatal("the rebound key does not create a task")
	}
	// And the key it replaced no longer does.
	if keys.action("c") == ActionNewTask {
		t.Fatal("the old key still creates a task")
	}
	// Everything untouched is left alone.
	if keys.key(ActionQuit) != "q" {
		t.Fatal("an unrelated binding moved")
	}
}

// A binding that does nothing and says nothing is worse than one refused.
func TestBadBindingsAreReported(t *testing.T) {
	keys, complaints := newBindings(map[string]string{
		"new-tsak":  "N",
		"new-shell": "  ",
	})
	if len(complaints) != 2 {
		t.Fatalf("complaints were %v", complaints)
	}
	if !strings.Contains(strings.Join(complaints, " "), "new-tsak") {
		t.Fatalf("the misspelled action was not named: %v", complaints)
	}
	// The rest of the map still works.
	if keys.key(ActionNewShell) != "n" {
		t.Fatal("a rejected override changed the default")
	}
}

// Two actions on one key means one of them silently stops working, so it is
// reported and resolved the same way every run.
func TestClashesAreReportedAndStable(t *testing.T) {
	var first string
	for attempt := range 20 {
		keys, complaints := newBindings(map[string]string{"new-shell": "c"})
		if len(complaints) != 1 || !strings.Contains(complaints[0], "bound to both") {
			t.Fatalf("the clash was not reported: %v", complaints)
		}
		winner := string(keys.action("c"))
		if attempt == 0 {
			first = winner
		}
		if winner != first {
			t.Fatalf("the winner changed between runs: %q then %q", first, winner)
		}
	}
}

// Help text reads from the same map as the keyboard, or it starts lying the
// moment somebody rebinds anything.
func TestHelpReadsTheBindings(t *testing.T) {
	m := Model{width: 160, height: 48}
	var complaints []string
	m.keys, complaints = newBindings(map[string]string{"new-task": "N", "diff": "D"})
	if len(complaints) != 0 {
		t.Fatalf("complaints: %v", complaints)
	}
	m.focus = focusTasks
	help := m.helpLine()
	if !strings.Contains(help, "N ") {
		t.Fatalf("the help line does not show the rebound key:\n%s", help)
	}
	if strings.Contains(help, "c new") {
		t.Fatalf("the help line still shows the old key:\n%s", help)
	}
}
