package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/martintrifunov/orkestar/internal/daemon"
)

// searchResultsMsg carries the answers to one query. machineID guards the
// reply the way every other overlay reply is guarded: a machine switch while
// a search is in flight must not install results from the machine left behind.
type searchResultsMsg struct {
	results   []daemon.SearchResult
	machineID string
	err       error
}

// openSearch shows the overlay with an empty query. Results are collected when
// the query is submitted rather than as it is typed: the daemon's search is a
// scan, and firing one per keystroke would spend more than it returns.
func (m *Model) openSearch() {
	m.searchOpen = true
	m.searchQuery = ""
	m.searchResults = nil
	m.searchAt = 0
	m.searchErr = nil
	m.notice = ""
}

// updateSearch handles the query line and the result list. Enter runs the
// query when there is nothing to select, and jumps to the selected result
// when there is.
func (m Model) updateSearch(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch k.Code {
	case tea.KeyEscape:
		m.searchOpen = false
		return m, nil
	case tea.KeyEnter:
		if len(m.searchResults) > 0 {
			return m.jumpToSearchResult()
		}
		if strings.TrimSpace(m.searchQuery) == "" {
			return m, nil
		}
		return m, m.runSearch()
	case tea.KeyUp:
		m.searchAt = max(0, m.searchAt-1)
		return m, nil
	case tea.KeyDown:
		if m.searchAt+1 < len(m.searchResults) {
			m.searchAt++
		}
		return m, nil
	case tea.KeyBackspace:
		if runes := []rune(m.searchQuery); len(runes) > 0 {
			m.searchQuery = string(runes[:len(runes)-1])
			m.searchResults, m.searchAt, m.searchErr = nil, 0, nil
		}
		return m, nil
	}
	if k.Text != "" && k.Mod&(tea.ModCtrl|tea.ModAlt) == 0 {
		m.searchQuery += k.Text
		m.searchResults, m.searchAt, m.searchErr = nil, 0, nil
	}
	return m, nil
}

// runSearch queries the daemon and adds matches the daemon cannot see: pane
// labels are client-side, so they are matched here.
func (m Model) runSearch() tea.Cmd {
	query := strings.TrimSpace(m.searchQuery)
	machineID := m.currentMachine().ID
	local := m.localSearchResults(query)
	client := m.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		var output struct {
			Results []daemon.SearchResult `json:"results"`
		}
		err := client.Call(ctx, "search.query", map[string]any{
			"query": query, "limit": 100,
		}, &output)
		if err != nil {
			return searchResultsMsg{machineID: machineID, err: err}
		}
		return searchResultsMsg{machineID: machineID, results: append(local, output.Results...)}
	}
}

// localSearchResults matches the labels of the panes on screen, which the
// daemon has no view of.
func (m Model) localSearchResults(query string) []daemon.SearchResult {
	needle := strings.ToLower(query)
	var results []daemon.SearchResult
	for _, pane := range m.visiblePanes() {
		label := m.paneLabel(pane)
		if !strings.Contains(strings.ToLower(label), needle) {
			continue
		}
		results = append(results, daemon.SearchResult{
			Kind: "pane", ID: pane.terminalID, Title: label, Snippet: label,
		})
	}
	return results
}

// jumpToSearchResult focuses whatever the selected result is about. A result
// whose target is gone is reported rather than silently doing nothing.
func (m Model) jumpToSearchResult() (tea.Model, tea.Cmd) {
	if m.searchAt < 0 || m.searchAt >= len(m.searchResults) {
		return m, nil
	}
	result := m.searchResults[m.searchAt]
	m.searchOpen = false
	switch result.Kind {
	case "pane", "terminal":
		if m.findPane(result.ID) == nil && result.Kind == "terminal" {
			return m, m.focusOrOpen(result.ID)
		}
		if pane := m.findPane(result.ID); pane != nil {
			m.embedded = pane
			m.sidebarFocused = false
			m.filesFocused = false
			return m, nil
		}
		return m, m.focusOrOpen(result.ID)
	case "task", "artifact":
		for index, task := range m.snapshot.Tasks {
			if task.ID == result.ID || result.Kind == "artifact" && task.ID == m.artifactTask(result.ID) {
				m.taskSelected = index
				m.focus = focusTasks
				m.sidebarFocused = true
				return m, nil
			}
		}
		m.notice = "That task is no longer on the board."
	case "template":
		m.notice = fmt.Sprintf("Template %q: apply it from the Tasks section.", result.ID)
	}
	return m, nil
}

// artifactTask finds the task an artifact belongs to, for jumping to it.
func (m Model) artifactTask(artifactID string) string {
	for _, artifact := range m.snapshot.Artifacts {
		if artifact.ID == artifactID {
			return artifact.TaskID
		}
	}
	return ""
}

// searchView renders the query line and the results, one row per hit.
func (m Model) searchView() string {
	var lines []string
	lines = append(lines, "Search", "", "Query: "+m.searchQuery+"▏", "")
	if m.searchErr != nil {
		lines = append(lines, m.theme.error.Render(m.searchErr.Error()))
	} else if len(m.searchResults) == 0 {
		lines = append(lines, "Enter runs the search over panes, tasks and artifacts.")
	} else {
		visible := max(1, m.height-14)
		top := max(0, m.searchAt-visible+1)
		for index := top; index < min(len(m.searchResults), top+visible); index++ {
			result := m.searchResults[index]
			location := result.ID
			if result.Line > 0 {
				location = fmt.Sprintf("%s:%d", result.ID, result.Line)
			}
			row := fmt.Sprintf("%-9s %-18s %s", result.Kind, location, result.Snippet)
			if index == m.searchAt {
				row = m.theme.selected.Render(row)
			}
			lines = append(lines, row)
		}
	}
	lines = append(lines, "", "↑/↓ select · Enter search or open · Esc close")
	return strings.Join(lines, "\n")
}
