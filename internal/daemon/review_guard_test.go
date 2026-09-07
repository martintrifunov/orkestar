package daemon

import (
	"context"
	"testing"

	"github.com/martintrifunov/orkestar/internal/workflow"
)

func TestReviewRejectsAmbiguousVerdicts(t *testing.T) {
	for _, text := range []string{"VERDICT: APPROVE_NOT", "VERDICT: APPROVE?", "Example:\nVERDICT: APPROVE\nVERDICT: REJECT"} {
		if parseReviewVerdict(text).Approved {
			t.Fatalf("approved %q", text)
		}
	}
	if !parseReviewVerdict("VERDICT: APPROVE\nLooks good").Approved {
		t.Fatal("valid approval rejected")
	}
}

// TestReviewApprovesATaskWithNoWorktree pins the case that used to fail
// closed by mistake: a task with no worktree has no diff, the same as one
// with no uncommitted changes, so there is nothing for a reviewer to judge.
func TestReviewApprovesATaskWithNoWorktree(t *testing.T) {
	s := NewServer("")
	outcome, err := s.runAutomaticReview(context.Background(), workflow.Task{ID: "t"})
	if err != nil {
		t.Fatalf("task with no worktree should have nothing to review: %v", err)
	}
	if !outcome.Approved {
		t.Fatal("task with no worktree was not approved")
	}
}

// TestReviewFailsClosedOnceAWorktreeExists covers the case worth failing
// closed on: a worktree means there may be real changes riding on this
// verdict, so a missing reviewer or a missing workspace must not silently
// bypass the gate the way an absent worktree does.
func TestReviewFailsClosedOnceAWorktreeExists(t *testing.T) {
	s := NewServer("")
	task := workflow.Task{ID: "t", WorktreePath: "/tmp/does-not-matter"}
	if _, err := s.runAutomaticReview(context.Background(), task); err == nil {
		t.Fatal("missing reviewer bypassed review for a task with a worktree")
	}
	s.SetReviewerAdapter("fixture")
	if _, err := s.runAutomaticReview(context.Background(), task); err == nil {
		t.Fatal("missing workspace bypassed review for a task with a worktree")
	}
}
