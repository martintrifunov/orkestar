package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/martintrifunov/orkestar/internal/git"
	"github.com/martintrifunov/orkestar/internal/workflow"
)

func (s *Server) createTask(rawParams json.RawMessage) (workflow.Task, error) {
	var params struct {
		WorkspaceID string   `json:"workspace_id"`
		Title       string   `json:"title"`
		Description string   `json:"description"`
		DependsOn   []string `json:"depends_on"`
	}
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return workflow.Task{}, fmt.Errorf("decode task create params: %w", err)
	}

	s.mu.RLock()
	_, ok := s.workspaces[params.WorkspaceID]
	s.mu.RUnlock()
	if !ok {
		return workflow.Task{}, fmt.Errorf("workspace %q does not exist", params.WorkspaceID)
	}

	return s.tasks.Create(params.WorkspaceID, params.Title, params.Description, params.DependsOn)
}

func (s *Server) setTaskStatus(rawParams json.RawMessage) (workflow.Task, error) {
	var params struct {
		TaskID string `json:"task_id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return workflow.Task{}, fmt.Errorf("decode task update params: %w", err)
	}
	return s.tasks.SetStatus(params.TaskID, workflow.Status(params.Status))
}

func (s *Server) assignTask(rawParams json.RawMessage) (workflow.Task, error) {
	var params struct {
		TaskID  string `json:"task_id"`
		AgentID string `json:"agent_id"`
	}
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return workflow.Task{}, fmt.Errorf("decode task assign params: %w", err)
	}
	return s.tasks.Assign(params.TaskID, params.AgentID)
}

// createTaskWorktree gives a task its own git worktree and branch,
// sibling to its workspace's directory, so an agent can work on it without
// disturbing the workspace's primary checkout.
func (s *Server) createTaskWorktree(ctx context.Context, rawParams json.RawMessage) (workflow.Task, error) {
	var params struct {
		TaskID string `json:"task_id"`
		Branch string `json:"branch"`
	}
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return workflow.Task{}, fmt.Errorf("decode task worktree params: %w", err)
	}

	task, err := s.tasks.Get(params.TaskID)
	if err != nil {
		return workflow.Task{}, err
	}

	s.mu.RLock()
	workspace, ok := s.workspaces[task.WorkspaceID]
	s.mu.RUnlock()
	if !ok {
		return workflow.Task{}, fmt.Errorf("workspace %q does not exist", task.WorkspaceID)
	}

	branch := params.Branch
	if branch == "" {
		branch = "task/" + task.ID
	}
	worktreePath := filepath.Join(filepath.Dir(workspace.Directory), filepath.Base(workspace.Directory)+"-worktrees", task.ID)

	if err := git.AddWorktree(ctx, workspace.Directory, worktreePath, branch); err != nil {
		return workflow.Task{}, fmt.Errorf("create task worktree: %w", err)
	}
	return s.tasks.SetWorktree(task.ID, worktreePath, branch)
}

// TaskDiff is the changed-file summary and unified diff for a task's
// worktree.
type TaskDiff struct {
	Files []git.ChangedFile `json:"files"`
	Diff  string            `json:"diff"`
}

// taskDiff returns the changed files and diff for a task's worktree. The
// task must already have one, created via task.createWorktree.
func (s *Server) taskDiff(ctx context.Context, rawParams json.RawMessage) (TaskDiff, error) {
	var params struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return TaskDiff{}, fmt.Errorf("decode task diff params: %w", err)
	}

	task, err := s.tasks.Get(params.TaskID)
	if err != nil {
		return TaskDiff{}, err
	}
	if task.WorktreePath == "" {
		return TaskDiff{}, fmt.Errorf("task %q has no worktree", task.ID)
	}

	files, err := git.ChangedFiles(ctx, task.WorktreePath)
	if err != nil {
		return TaskDiff{}, fmt.Errorf("list changed files: %w", err)
	}
	diff, err := git.Diff(ctx, task.WorktreePath)
	if err != nil {
		return TaskDiff{}, fmt.Errorf("diff worktree: %w", err)
	}
	return TaskDiff{Files: files, Diff: diff}, nil
}

// removeTaskWorktree removes a task's git worktree from disk and clears
// its worktree metadata.
func (s *Server) removeTaskWorktree(ctx context.Context, rawParams json.RawMessage) (workflow.Task, error) {
	var params struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return workflow.Task{}, fmt.Errorf("decode task worktree params: %w", err)
	}

	task, err := s.tasks.Get(params.TaskID)
	if err != nil {
		return workflow.Task{}, err
	}
	if task.WorktreePath == "" {
		return workflow.Task{}, fmt.Errorf("task %q has no worktree", task.ID)
	}

	s.mu.RLock()
	workspace, ok := s.workspaces[task.WorkspaceID]
	s.mu.RUnlock()
	if !ok {
		return workflow.Task{}, fmt.Errorf("workspace %q does not exist", task.WorkspaceID)
	}

	if err := git.RemoveWorktree(ctx, workspace.Directory, task.WorktreePath); err != nil {
		return workflow.Task{}, fmt.Errorf("remove task worktree: %w", err)
	}
	return s.tasks.SetWorktree(task.ID, "", "")
}
