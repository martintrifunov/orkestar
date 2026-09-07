package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/martintrifunov/orkestar/internal/git"
	"github.com/martintrifunov/orkestar/internal/workflow"
)

func (s *Server) createTask(rawParams json.RawMessage) (workflow.Task, error) {
	var params struct {
		WorkspaceID string   `json:"workspace_id"`
		Title       string   `json:"title"`
		Description string   `json:"description"`
		DependsOn   []string `json:"depends_on"`
		// AutoReview defaults to true (a reviewer-agent verdict is
		// required before the task can move to done) unless the caller
		// explicitly opts out.
		AutoReview *bool `json:"auto_review"`
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

	autoReview := params.AutoReview == nil || *params.AutoReview
	return s.tasks.Create(params.WorkspaceID, params.Title, params.Description, params.DependsOn, autoReview)
}

func (s *Server) setTaskStatus(ctx context.Context, rawParams json.RawMessage) (workflow.Task, error) {
	var params struct {
		TaskID string `json:"task_id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return workflow.Task{}, fmt.Errorf("decode task update params: %w", err)
	}

	if workflow.Status(params.Status) == workflow.StatusDone {
		task, err := s.tasks.Get(params.TaskID)
		if err != nil {
			return workflow.Task{}, err
		}
		if task.AutoReview {
			outcome, err := s.runAutomaticReview(ctx, task)
			if err != nil {
				return workflow.Task{}, fmt.Errorf("automatic review: %w", err)
			}
			if !outcome.Approved {
				return workflow.Task{}, fmt.Errorf("reviewer requested changes: %s", outcome.Reason)
			}
		}
	}

	return s.tasks.SetStatus(params.TaskID, workflow.Status(params.Status))
}

// updateTask edits a task's title, description or dependencies. Every field is
// a pointer so an absent one means "leave it alone": a caller fixing a typo in
// a title must not blank the description it never sent.
func (s *Server) updateTask(rawParams json.RawMessage) (workflow.Task, error) {
	var params struct {
		TaskID      string    `json:"task_id"`
		Title       *string   `json:"title"`
		Description *string   `json:"description"`
		DependsOn   *[]string `json:"depends_on"`
	}
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return workflow.Task{}, fmt.Errorf("decode task update params: %w", err)
	}
	return s.tasks.Update(params.TaskID, workflow.TaskEdit{
		Title:       params.Title,
		Description: params.Description,
		DependsOn:   params.DependsOn,
	})
}

// maxWaitTimeout bounds how long a wait may hold its connection. A wait is a
// request that deliberately does not answer yet, so it needs a ceiling: an
// orchestrator that asks to wait forever should be told no rather than leak a
// connection and a watcher for the life of the daemon.
const (
	defaultWaitTimeout = 5 * time.Minute
	maxWaitTimeout     = time.Hour
)

// waitTimeout turns a caller's requested seconds into a bounded duration.
func waitTimeout(seconds int) (time.Duration, error) {
	if seconds < 0 {
		return 0, errors.New("wait timeout cannot be negative")
	}
	if seconds == 0 {
		return defaultWaitTimeout, nil
	}
	if seconds <= int(maxWaitTimeout/time.Second) {
		return time.Duration(seconds) * time.Second, nil
	}
	return 0, fmt.Errorf("wait timeout cannot exceed %s", maxWaitTimeout)
}

// waitForTask blocks until a task reaches a condition. It is the primitive an
// orchestrating agent has no substitute for: every other task method answers
// immediately, so watching another agent's work means asking again and again,
// and for an agent every ask costs a turn.
func (s *Server) waitForTask(ctx context.Context, rawParams json.RawMessage) (workflow.Task, error) {
	var params struct {
		TaskID  string `json:"task_id"`
		Until   string `json:"until"`
		Timeout int    `json:"timeout_seconds"`
	}
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return workflow.Task{}, fmt.Errorf("decode task wait params: %w", err)
	}
	timeout, err := waitTimeout(params.Timeout)
	if err != nil {
		return workflow.Task{}, err
	}
	until := workflow.Condition(params.Until)
	if params.Until == "" {
		until = workflow.ConditionFinished
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	// A shutdown must release the wait, or the daemon cannot stop until every
	// outstanding one has timed out.
	go guard("task.wait-cancel", func() {
		select {
		case <-s.stop:
			cancel()
		case <-ctx.Done():
		}
	})
	return s.tasks.WaitFor(ctx, params.TaskID, until)
}

// waitForAgentSession blocks until an agent reaches a condition.
func (s *Server) waitForAgentSession(ctx context.Context, rawParams json.RawMessage) (Agent, error) {
	var params struct {
		AgentID string `json:"agent_id"`
		Until   string `json:"until"`
		Timeout int    `json:"timeout_seconds"`
	}
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return Agent{}, fmt.Errorf("decode agent wait params: %w", err)
	}
	timeout, err := waitTimeout(params.Timeout)
	if err != nil {
		return Agent{}, err
	}
	until := AgentCondition(params.Until)
	if params.Until == "" {
		until = AgentIdle
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return s.waitForAgent(ctx, params.AgentID, until)
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
	return s.createWorktreeFor(ctx, task, params.Branch)
}

// createWorktreeFor gives a task its own checkout. Applying a template goes
// through here rather than building an IPC request to call itself with.
func (s *Server) createWorktreeFor(ctx context.Context, task workflow.Task, branch string) (workflow.Task, error) {
	s.mu.RLock()
	workspace, ok := s.workspaces[task.WorkspaceID]
	s.mu.RUnlock()
	if !ok {
		return workflow.Task{}, fmt.Errorf("workspace %q does not exist", task.WorkspaceID)
	}
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
