package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestSearchOverlayOpensAndAcceptsAQuery(t *testing.T) {
	model := Model{snapshot: sampleSnapshot(), width: 160, height: 42}

	updated, _ := model.Update(tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl})
	model = updated.(Model)
	updated, _ = model.Update(tea.KeyPressMsg{Code: '/'})
	model = updated.(Model)
	if !model.searchOpen {
		t.Fatal("ctrl+b / did not open the search overlay")
	}

	for _, character := range "needle" {
		updated, _ = model.Update(tea.KeyPressMsg{Text: string(character)})
		model = updated.(Model)
	}
	if model.searchQuery != "needle" {
		t.Fatalf("query is %q", model.searchQuery)
	}
	if !strings.Contains(model.searchView(), "needle") {
		t.Fatalf("the query is not on screen:\n%s", model.searchView())
	}

	updated, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	model = updated.(Model)
	if model.searchQuery != "needl" {
		t.Fatalf("backspace left %q", model.searchQuery)
	}

	updated, _ = model.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	model = updated.(Model)
	if model.searchOpen {
		t.Fatal("escape did not close the search overlay")
	}
}

func TestLocalSearchMatchesPaneLabels(t *testing.T) {
	model := Model{snapshot: sampleSnapshot(), width: 160, height: 42}
	model.addPane(fakePane(t, "term_needle"))

	results := model.localSearchResults("needle")
	if len(results) != 1 || results[0].Kind != "pane" || results[0].ID != "term_needle" {
		t.Fatalf("unexpected local results: %+v", results)
	}
	if absent := model.localSearchResults("nothing-here"); len(absent) != 0 {
		t.Fatalf("expected no results, got %+v", absent)
	}
}
