package daemon

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/martintrifunov/orkestar/internal/agent"
	"github.com/martintrifunov/orkestar/internal/git"
	"github.com/martintrifunov/orkestar/internal/workflow"
)

// maxReviewDiffChars bounds how much diff text is sent to the reviewer
// agent, so a very large change doesn't blow up the prompt.
const maxReviewDiffChars = 20000

// SetReviewerAdapter names the registered adapter used for automatic task
// review. It must already be registered via RegisterAdapter, support
// managed mode, and produce sessions implementing agent.ResponsiveSession
// (interactive PTY adapters, such as Claude Code today, cannot: there is
// no discrete reply to parse a verdict from). Automatic review is inert
// until this is called.
func (s *Server) SetReviewerAdapter(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reviewerAdapter = name
}

// reviewOutcome is the result of an automatic reviewer-agent run.
type reviewOutcome struct {
	Approved bool
	Reason   string
}

// runAutomaticReview asks the configured reviewer adapter to judge a task's
// diff. A task with no worktree has nothing to review, the same as one with
// no uncommitted changes, so it is approved outright. Once a worktree exists,
// though, there may be real changes riding on this verdict: a missing
// reviewer, a missing workspace, or an oversized diff all fail closed rather
// than silently bypassing the review gate.
func (s *Server) runAutomaticReview(ctx context.Context, task workflow.Task) (reviewOutcome, error) {
	if task.WorktreePath == "" {
		return reviewOutcome{Approved: true}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	s.mu.RLock()
	reviewerName := s.reviewerAdapter
	adapter, adapterOK := s.adapters[reviewerName]
	_, workspaceOK := s.workspaces[task.WorkspaceID]
	s.mu.RUnlock()

	if reviewerName == "" {
		return reviewOutcome{}, fmt.Errorf("no reviewer adapter is configured")
	}
	if !workspaceOK {
		return reviewOutcome{}, fmt.Errorf("workspace %q does not exist", task.WorkspaceID)
	}

	files, err := git.ChangedFiles(ctx, task.WorktreePath)
	if err != nil {
		return reviewOutcome{}, fmt.Errorf("review task %q: list changed files: %w", task.ID, err)
	}
	if len(files) == 0 {
		return reviewOutcome{Approved: true}, nil
	}
	diff, err := git.Diff(ctx, task.WorktreePath)
	if err != nil {
		return reviewOutcome{}, fmt.Errorf("review task %q: diff worktree: %w", task.ID, err)
	}
	if len(diff) > maxReviewDiffChars {
		return reviewOutcome{}, fmt.Errorf("diff exceeds automatic review limit of %d bytes; split the task before review", maxReviewDiffChars)
	}

	if !adapterOK {
		return reviewOutcome{}, fmt.Errorf("reviewer adapter %q is not registered", reviewerName)
	}

	session, err := adapter.Launch(ctx, agent.LaunchOptions{
		Mode:      agent.ModeManaged,
		Directory: task.WorktreePath,
	})
	if err != nil {
		return reviewOutcome{}, fmt.Errorf("review task %q: launch reviewer: %w", task.ID, err)
	}
	defer session.Close()

	responsive, ok := session.(agent.ResponsiveSession)
	if !ok {
		return reviewOutcome{}, fmt.Errorf("reviewer adapter %q does not support structured responses", reviewerName)
	}

	reply, err := responsive.PromptForResponse(ctx, buildReviewPrompt(task, diff))
	if err != nil {
		return reviewOutcome{}, fmt.Errorf("review task %q: prompt reviewer: %w", task.ID, err)
	}

	outcome := parseReviewVerdict(reply)

	if _, err := s.artifacts.Add(task.ID, workflow.ArtifactReview, "reviewer verdict", "", reply); err != nil {
		return reviewOutcome{}, fmt.Errorf("review task %q: record review artifact: %w", task.ID, err)
	}
	if err := s.persist(); err != nil {
		return reviewOutcome{}, err
	}
	return outcome, nil
}

func buildReviewPrompt(task workflow.Task, diff string) string {
	var prompt strings.Builder
	prompt.WriteString("You are reviewing a completed task before it is marked done.\n")
	prompt.WriteString("Task: " + task.Title + "\n")
	if task.Description != "" {
		prompt.WriteString("Description: " + task.Description + "\n")
	}
	prompt.WriteString("\nReview the following diff. Respond with a first line of exactly\n")
	prompt.WriteString("\"VERDICT: APPROVE\" or \"VERDICT: REJECT\", followed by your reasoning.\n\n")
	prompt.WriteString(diff)
	return prompt.String()
}

// parseReviewVerdict looks for a "VERDICT: APPROVE" or "VERDICT: REJECT"
// line. A reply that doesn't contain a recognizable verdict is treated as
// a rejection: blocking on an ambiguous review is safer than completing a
// task no one actually approved.
func parseReviewVerdict(reply string) reviewOutcome {
	line := strings.TrimSpace(strings.SplitN(strings.TrimSpace(reply), "\n", 2)[0])
	switch strings.ToUpper(line) {
	case "VERDICT: APPROVE":
		return reviewOutcome{Approved: true, Reason: reply}
	case "VERDICT: REJECT":
		return reviewOutcome{Approved: false, Reason: reply}
	}

	return reviewOutcome{Approved: false, Reason: "reviewer did not return a recognizable verdict:\n" + reply}
}
