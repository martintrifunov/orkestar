package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/martintrifunov/orkestar/internal/workflow"
)

// templateDirectory is where a workspace keeps its templates, under the
// workspace itself so they are committed beside the code they describe.
const templateDirectory = ".orkestar/templates"

// AppliedTemplate is what applying one produced: the tasks that now exist, and
// the agents started on them.
type AppliedTemplate struct {
	Template string          `json:"template"`
	Tasks    []workflow.Task `json:"tasks"`
	Agents   []Agent         `json:"agents,omitempty"`
	// Waiting names the tasks that declare an agent but were not started,
	// because something they depend on has not finished. Applying does not
	// wait around for them; task.wait until startable is how to pick them up.
	Waiting []string `json:"waiting,omitempty"`
	// Failed says why an agent could not be started, when the tasks were
	// created but a launch was not. It is carried in the result rather than
	// returned as an error: an error discards the result, so the caller would
	// never learn which tasks and worktrees now exist.
	Failed string `json:"failed,omitempty"`
}

// templatePath resolves a template name to a file inside the workspace,
// refusing anything that climbs out of the template directory. The name
// crosses an IPC boundary and an agent may supply it.
func templatePath(directory, name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("template name is required")
	}
	if strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
		return "", fmt.Errorf("template name %q cannot be a path", name)
	}
	root := filepath.Join(directory, templateDirectory)
	path := filepath.Join(root, name+".json")
	// Belt and braces: even with the checks above, the result must sit inside
	// the directory it is supposed to.
	if relative, err := filepath.Rel(root, path); err != nil || strings.HasPrefix(relative, "..") {
		return "", fmt.Errorf("template name %q cannot be a path", name)
	}
	return path, nil
}

// listTemplates reports the templates a workspace defines. A workspace with no
// template directory has none, which is not an error.
func (s *Server) listTemplates(rawParams json.RawMessage) (map[string]any, error) {
	var params struct {
		WorkspaceID string `json:"workspace_id"`
	}
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return nil, fmt.Errorf("decode template list params: %w", err)
	}
	s.mu.RLock()
	workspace, ok := s.workspaces[params.WorkspaceID]
	s.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("workspace %q does not exist", params.WorkspaceID)
	}

	entries, err := os.ReadDir(filepath.Join(workspace.Directory, templateDirectory))
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]any{"templates": []workflow.Template{}}, nil
		}
		return nil, fmt.Errorf("read templates: %w", err)
	}

	templates := []workflow.Template{}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".json")
		template, err := s.readTemplate(workspace.Directory, name)
		if err != nil {
			// One unreadable file must not hide the rest. The name is still
			// reported, with the reason in place of a description, so it is
			// visible rather than silently missing.
			templates = append(templates, workflow.Template{Name: name, Description: "cannot be used: " + err.Error()})
			continue
		}
		templates = append(templates, template)
	}
	sort.Slice(templates, func(left, right int) bool { return templates[left].Name < templates[right].Name })
	return map[string]any{"templates": templates}, nil
}

func (s *Server) readTemplate(directory, name string) (workflow.Template, error) {
	path, err := templatePath(directory, name)
	if err != nil {
		return workflow.Template{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return workflow.Template{}, fmt.Errorf("workspace has no template %q", name)
		}
		return workflow.Template{}, fmt.Errorf("read template %q: %w", name, err)
	}
	return workflow.ParseTemplate(data)
}

// applyTemplate creates the template's tasks and, when asked, starts the
// agents it names.
//
// Only tasks with nothing blocking them are started. Starting the rest would
// mean launching agents to sit idle against work they cannot begin, so they
// are reported as waiting and picked up with task.wait until startable, which
// is what that condition is for.
func (s *Server) applyTemplate(ctx context.Context, rawParams json.RawMessage) (AppliedTemplate, error) {
	var params struct {
		WorkspaceID string `json:"workspace_id"`
		Name        string `json:"name"`
		Start       bool   `json:"start"`
	}
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return AppliedTemplate{}, fmt.Errorf("decode template apply params: %w", err)
	}
	s.mu.RLock()
	workspace, ok := s.workspaces[params.WorkspaceID]
	s.mu.RUnlock()
	if !ok {
		return AppliedTemplate{}, fmt.Errorf("workspace %q does not exist", params.WorkspaceID)
	}

	template, err := s.readTemplate(workspace.Directory, params.Name)
	if err != nil {
		return AppliedTemplate{}, err
	}
	ordered, err := template.Ordered()
	if err != nil {
		return AppliedTemplate{}, err
	}

	// Checked before anything is created: an adapter named but not registered
	// would otherwise leave tasks on the board and fail partway through.
	if params.Start {
		s.mu.RLock()
		for _, task := range ordered {
			if task.Agent == "" {
				continue
			}
			if _, ok := s.adapters[task.Agent]; !ok {
				s.mu.RUnlock()
				return AppliedTemplate{}, fmt.Errorf("template names adapter %q, which is not registered", task.Agent)
			}
		}
		s.mu.RUnlock()
	}

	applied := AppliedTemplate{Template: template.Name}
	created := make(map[string]workflow.Task, len(ordered))
	for _, declared := range ordered {
		dependsOn := make([]string, 0, len(declared.DependsOn))
		for _, key := range declared.DependsOn {
			dependsOn = append(dependsOn, created[key].ID)
		}
		task, err := s.tasks.Create(params.WorkspaceID, declared.Title, declared.Description, dependsOn, declared.ReviewRequired())
		if err != nil {
			applied.Failed = fmt.Sprintf("create %q: %v", declared.Key, err)
			return applied, nil
		}
		if declared.Worktree {
			withWorktree, err := s.createWorktreeFor(ctx, task, "")
			if err != nil {
				applied.Tasks = append(applied.Tasks, task)
				applied.Failed = fmt.Sprintf("worktree for %q: %v", declared.Key, err)
				return applied, nil
			}
			task = withWorktree
		}
		created[declared.Key] = task
		applied.Tasks = append(applied.Tasks, task)
	}

	if !params.Start {
		return applied, nil
	}
	for _, declared := range ordered {
		if declared.Agent == "" {
			continue
		}
		task := created[declared.Key]
		if len(task.DependsOn) > 0 {
			applied.Waiting = append(applied.Waiting, task.ID)
			continue
		}
		prompt := declared.Prompt
		if prompt == "" {
			prompt = "You have been assigned this task: " + task.Title
			if task.Description != "" {
				prompt += "\n\n" + task.Description
			}
		}
		launched, err := s.launchForTask(ctx, task, declared.Agent, prompt)
		if err != nil {
			// Reported in the result, not as an error. The tasks and their
			// worktrees are already real, and an error would discard the
			// result and skip the persist that records them, leaving the
			// caller with no idea what now exists.
			applied.Failed = fmt.Sprintf("start %q: %v", declared.Key, err)
			return applied, nil
		}
		applied.Agents = append(applied.Agents, launched)
	}
	return applied, nil
}
