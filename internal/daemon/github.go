package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/martintrifunov/orkestar/internal/github"
	"github.com/martintrifunov/orkestar/internal/workflow"
)

// GitHubImport is what importing an issue produced: the task and the issue it
// came from.
type GitHubImport struct {
	Task   workflow.Task `json:"task"`
	URL    string        `json:"url"`
	Number int           `json:"number"`
}

// githubImport creates a task from a GitHub issue. gh owns authentication, so
// Orkestar never reads or stores a token; a missing or unauthenticated gh is
// reported before anything is created.
func (s *Server) githubImport(ctx context.Context, rawParams json.RawMessage) (GitHubImport, error) {
	var params struct {
		WorkspaceID string `json:"workspace_id"`
		Reference   string `json:"reference"`
	}
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return GitHubImport{}, fmt.Errorf("decode github import params: %w", err)
	}
	if strings.TrimSpace(params.Reference) == "" {
		return GitHubImport{}, errors.New("an issue number or URL is required")
	}
	s.mu.RLock()
	workspace, ok := s.workspaces[params.WorkspaceID]
	s.mu.RUnlock()
	if !ok {
		return GitHubImport{}, fmt.Errorf("workspace %q does not exist", params.WorkspaceID)
	}

	issue, err := github.ViewIssue(ctx, workspace.Directory, params.Reference)
	if err != nil {
		return GitHubImport{}, err
	}
	description := issue.Body
	if description != "" {
		description += "\n\n"
	}
	description += fmt.Sprintf("Imported from %s", issue.URL)
	task, err := s.tasks.Create(params.WorkspaceID, issue.Title, description, nil, true)
	if err != nil {
		return GitHubImport{}, err
	}
	return GitHubImport{Task: task, URL: issue.URL, Number: issue.Number}, nil
}

// githubPullRequest opens a pull request for a completed task's worktree. It
// refuses an unfinished task: a PR is the reviewed result, not a draft of it.
func (s *Server) githubPullRequest(ctx context.Context, rawParams json.RawMessage) (workflow.Artifact, error) {
	var params struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return workflow.Artifact{}, fmt.Errorf("decode github pull request params: %w", err)
	}
	task, err := s.tasks.Get(params.TaskID)
	if err != nil {
		return workflow.Artifact{}, err
	}
	if task.Status != workflow.StatusDone {
		return workflow.Artifact{}, fmt.Errorf("task %q is %s; a pull request is opened from a finished task", task.ID, task.Status)
	}
	if task.WorktreePath == "" || task.WorktreeBranch == "" {
		return workflow.Artifact{}, fmt.Errorf("task %q has no worktree branch to open a pull request from", task.ID)
	}

	body := task.Description
	if body != "" {
		body += "\n\n"
	}
	body += fmt.Sprintf("Opened from Orkestar task %s.", task.ID)
	for _, artifact := range s.artifacts.ForTask(task.ID) {
		if artifact.Kind == workflow.ArtifactReview {
			body += "\n\nReviewer verdict:\n" + artifact.Content
		}
	}

	url, err := github.CreatePullRequest(ctx, task.WorktreePath, task.Title, body, task.WorktreeBranch)
	if err != nil {
		return workflow.Artifact{}, err
	}
	artifact, err := s.artifacts.Add(task.ID, workflow.ArtifactPullRequest, "pull request", url, "")
	if err != nil {
		return workflow.Artifact{}, err
	}
	_ = s.persist()
	return artifact, nil
}
