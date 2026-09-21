package daemon

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/martintrifunov/orkestar/internal/workflow"
)

// SearchResult is one match from a global search. Kind says where it came
// from, which is also what a client uses to decide what jumping to it means.
type SearchResult struct {
	Kind    string    `json:"kind"` // terminal, task, artifact or template
	ID      string    `json:"id"`
	Title   string    `json:"title,omitempty"`
	Snippet string    `json:"snippet"`
	Line    int       `json:"line,omitempty"`
	At      time.Time `json:"at,omitempty"`
}

const (
	defaultSearchLimit = 100
	maxSearchLimit     = 500
	searchSnippetWidth = 200
)

// searchKindOrder is the deterministic order results are reported in: where
// the output is, then what was asked for, then the evidence, then the recipe.
var searchKindOrder = map[string]int{"terminal": 0, "task": 1, "artifact": 2, "template": 3}

// searchQuery searches every place Orkestar keeps text: the visible screen and
// bounded scrollback of each terminal, tasks, artifacts and templates. It is a
// scan rather than an index because the history is already capped and local.
func (s *Server) searchQuery(raw json.RawMessage) (map[string]any, error) {
	var params struct {
		Query       string `json:"query"`
		WorkspaceID string `json:"workspace_id"`
		Limit       int    `json:"limit"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &params); err != nil {
			return nil, err
		}
	}
	query := strings.TrimSpace(params.Query)
	if query == "" {
		return nil, errors.New("search query is required")
	}
	limit := params.Limit
	if limit <= 0 {
		limit = defaultSearchLimit
	}
	if limit > maxSearchLimit {
		limit = maxSearchLimit
	}
	needle := strings.ToLower(query)

	results := make([]SearchResult, 0, limit)
	results = append(results, s.searchTerminals(params.WorkspaceID, needle)...)
	tasks := s.tasks.List()
	results = append(results, searchTasks(tasks, params.WorkspaceID, needle)...)
	results = append(results, searchArtifacts(s.artifacts.List(), tasks, params.WorkspaceID, needle)...)
	results = append(results, s.searchTemplates(params.WorkspaceID, needle)...)

	sort.SliceStable(results, func(left, right int) bool {
		if searchKindOrder[results[left].Kind] != searchKindOrder[results[right].Kind] {
			return searchKindOrder[results[left].Kind] < searchKindOrder[results[right].Kind]
		}
		if !results[left].At.Equal(results[right].At) {
			return results[left].At.After(results[right].At)
		}
		if results[left].ID != results[right].ID {
			return results[left].ID < results[right].ID
		}
		return results[left].Line < results[right].Line
	})
	if len(results) > limit {
		results = results[:limit]
	}
	return map[string]any{"query": query, "results": results}, nil
}

// searchTerminals matches the visible screen and bounded scrollback of every
// terminal. The screen is read outside the server lock because rendering it
// takes the session's own.
func (s *Server) searchTerminals(workspaceID, needle string) []SearchResult {
	type reference struct {
		session *terminalSession
		meta    Terminal
	}
	s.mu.RLock()
	references := make([]reference, 0, len(s.terminals))
	for _, session := range s.terminals {
		meta := session.snapshot()
		if workspaceID != "" && meta.WorkspaceID != workspaceID {
			continue
		}
		references = append(references, reference{session: session, meta: meta})
	}
	s.mu.RUnlock()

	var results []SearchResult
	for _, reference := range references {
		text := reference.session.text(maxReadLines)
		for index, line := range strings.Split(text, "\n") {
			if !strings.Contains(strings.ToLower(line), needle) {
				continue
			}
			results = append(results, SearchResult{
				Kind:    "terminal",
				ID:      reference.meta.ID,
				Title:   strings.Join(reference.meta.Command, " "),
				Snippet: searchSnippet(line),
				Line:    index + 1,
				At:      reference.meta.CreatedAt,
			})
		}
	}
	return results
}

func searchTasks(tasks []workflow.Task, workspaceID, needle string) []SearchResult {
	var results []SearchResult
	for _, task := range tasks {
		if workspaceID != "" && task.WorkspaceID != workspaceID {
			continue
		}
		for _, field := range []string{task.Title, task.Description, task.ID} {
			if field != "" && strings.Contains(strings.ToLower(field), needle) {
				results = append(results, SearchResult{
					Kind: "task", ID: task.ID, Title: task.Title,
					Snippet: searchSnippet(field), At: task.CreatedAt,
				})
				break
			}
		}
	}
	return results
}

func searchArtifacts(artifacts []workflow.Artifact, tasks []workflow.Task, workspaceID, needle string) []SearchResult {
	workspaceOf := make(map[string]string, len(tasks))
	for _, task := range tasks {
		workspaceOf[task.ID] = task.WorkspaceID
	}
	var results []SearchResult
	for _, artifact := range artifacts {
		if workspaceID != "" && workspaceOf[artifact.TaskID] != workspaceID {
			continue
		}
		for _, field := range []string{artifact.Label, artifact.Path, artifact.Content} {
			if field != "" && strings.Contains(strings.ToLower(field), needle) {
				results = append(results, SearchResult{
					Kind: "artifact", ID: artifact.ID, Title: artifact.Label,
					Snippet: searchSnippet(field), At: artifact.CreatedAt,
				})
				break
			}
		}
	}
	return results
}

// searchTemplates reads each workspace's template directory. Templates are
// files rather than daemon state, so this is the one search that touches disk.
func (s *Server) searchTemplates(workspaceID, needle string) []SearchResult {
	s.mu.RLock()
	directories := make([]string, 0, len(s.workspaces))
	for _, workspace := range s.workspaces {
		if workspaceID != "" && workspace.ID != workspaceID {
			continue
		}
		directories = append(directories, workspace.Directory)
	}
	s.mu.RUnlock()

	var results []SearchResult
	for _, directory := range directories {
		entries, err := os.ReadDir(filepath.Join(directory, templateDirectory))
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
				continue
			}
			name := strings.TrimSuffix(entry.Name(), ".json")
			template, err := s.readTemplate(directory, name)
			if err != nil {
				continue
			}
			for _, field := range []string{template.Name, template.Description} {
				if field != "" && strings.Contains(strings.ToLower(field), needle) {
					results = append(results, SearchResult{
						Kind: "template", ID: template.Name, Title: template.Name, Snippet: searchSnippet(field),
					})
					break
				}
			}
		}
	}
	return results
}

// searchSnippet trims a matching line down to something a result row can show.
func searchSnippet(line string) string {
	line = strings.TrimSpace(strings.ReplaceAll(line, "\t", " "))
	if len(line) > searchSnippetWidth {
		line = line[:searchSnippetWidth]
	}
	return line
}
