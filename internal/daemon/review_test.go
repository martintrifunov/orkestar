package daemon_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/martintrifunov/orkestar/internal/agent"
	"github.com/martintrifunov/orkestar/internal/daemon"
	"github.com/martintrifunov/orkestar/internal/ipc"
	"github.com/martintrifunov/orkestar/internal/workflow"
)

// scriptedReviewer is a minimal agent.Adapter/ResponsiveSession pair that
// returns a canned reply to every prompt, so tests can drive the
// reviewer-agent gate without a real OpenCode server.
type scriptedReviewer struct {
	reply string
}

func (a *scriptedReviewer) Capabilities() agent.Capabilities {
	return agent.Capabilities{Name: "scripted-reviewer", SupportsManaged: true, SupportsPrompt: true}
}

func (a *scriptedReviewer) Launch(ctx context.Context, options agent.LaunchOptions) (agent.Session, error) {
	return &scriptedReviewerSession{reply: a.reply, events: make(chan agent.LifecycleEvent, 4)}, nil
}

type scriptedReviewerSession struct {
	reply  string
	events chan agent.LifecycleEvent
}

func (s *scriptedReviewerSession) ID() string              { return "scripted-session" }
func (s *scriptedReviewerSession) NativeSessionID() string { return "scripted-session" }
func (s *scriptedReviewerSession) State() agent.State      { return agent.StateReady }
func (s *scriptedReviewerSession) Prompt(ctx context.Context, text string) error {
	_, err := s.PromptForResponse(ctx, text)
	return err
}
func (s *scriptedReviewerSession) PromptForResponse(ctx context.Context, text string) (string, error) {
	return s.reply, nil
}
func (s *scriptedReviewerSession) Interrupt(ctx context.Context) error { return nil }
func (s *scriptedReviewerSession) Events() <-chan agent.LifecycleEvent { return s.events }
func (s *scriptedReviewerSession) Close() error                        { close(s.events); return nil }

var _ agent.ResponsiveSession = (*scriptedReviewerSession)(nil)

func newReviewTestServer(t *testing.T, reply string) (*ipc.Client, string, func()) {
	t.Helper()

	repoDirectory := initRepo(t)
	socketDirectory, err := os.MkdirTemp("/tmp", "orkestar-review-test-")
	if err != nil {
		t.Fatalf("create socket directory: %v", err)
	}

	server := daemon.NewServer(filepath.Join(socketDirectory, "orkestar.sock"))
	server.RegisterAdapter(&scriptedReviewer{reply: reply})
	server.SetReviewerAdapter("scripted-reviewer")

	ctx, cancel := context.WithCancel(context.Background())
	serverError := make(chan error, 1)
	go func() { serverError <- server.Serve(ctx) }()

	client := ipc.NewClient(filepath.Join(socketDirectory, "orkestar.sock"))
	waitForServer(t, client)

	cleanup := func() {
		cancel()
		if err := <-serverError; err != nil {
			t.Errorf("server shutdown: %v", err)
		}
		_ = os.RemoveAll(socketDirectory)
	}
	return client, repoDirectory, cleanup
}

func createTaskWithChanges(t *testing.T, client *ipc.Client, ctx context.Context, repoDirectory string) workflow.Task {
	t.Helper()

	var workspace daemon.Workspace
	if err := client.Call(ctx, "workspace.create", map[string]string{"directory": repoDirectory}, &workspace); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	var task workflow.Task
	if err := client.Call(ctx, "task.create", map[string]any{
		"workspace_id": workspace.ID,
		"title":        "reviewed work",
	}, &task); err != nil {
		t.Fatalf("create task: %v", err)
	}
	var withWorktree workflow.Task
	if err := client.Call(ctx, "task.createWorktree", map[string]string{"task_id": task.ID}, &withWorktree); err != nil {
		t.Fatalf("create task worktree: %v", err)
	}
	if err := os.WriteFile(filepath.Join(withWorktree.WorktreePath, "README.md"), []byte("hello\nreviewed change\n"), 0o644); err != nil {
		t.Fatalf("modify worktree file: %v", err)
	}
	return withWorktree
}

func TestAutomaticReviewApprovesAndUnblocksDone(t *testing.T) {
	t.Parallel()

	client, repoDirectory, cleanup := newReviewTestServer(t, "VERDICT: APPROVE\nLooks good.")
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	task := createTaskWithChanges(t, client, ctx, repoDirectory)

	var done workflow.Task
	if err := client.Call(ctx, "task.setStatus", map[string]string{
		"task_id": task.ID,
		"status":  string(workflow.StatusDone),
	}, &done); err != nil {
		t.Fatalf("set status to done after approval: %v", err)
	}
	if done.Status != workflow.StatusDone {
		t.Fatalf("unexpected status: %q", done.Status)
	}

	var snapshot daemon.Snapshot
	if err := client.Call(ctx, "system.snapshot", nil, &snapshot); err != nil {
		t.Fatalf("get snapshot: %v", err)
	}
	found := false
	for _, artifact := range snapshot.Artifacts {
		if artifact.TaskID == task.ID && artifact.Kind == workflow.ArtifactReview {
			found = true
			if artifact.Content == "" {
				t.Fatalf("expected review artifact to record the reply")
			}
		}
	}
	if !found {
		t.Fatalf("expected a review artifact to be recorded, got %#v", snapshot.Artifacts)
	}
}

func TestAutomaticReviewBlocksOnRejection(t *testing.T) {
	t.Parallel()

	client, repoDirectory, cleanup := newReviewTestServer(t, "VERDICT: REJECT\nMissing tests.")
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	task := createTaskWithChanges(t, client, ctx, repoDirectory)

	var done workflow.Task
	if err := client.Call(ctx, "task.setStatus", map[string]string{
		"task_id": task.ID,
		"status":  string(workflow.StatusDone),
	}, &done); err == nil {
		t.Fatal("expected rejection to block the done transition")
	}

	var fetched daemon.Snapshot
	if err := client.Call(ctx, "system.snapshot", nil, &fetched); err != nil {
		t.Fatalf("get snapshot: %v", err)
	}
	for _, snapshotTask := range fetched.Tasks {
		if snapshotTask.ID == task.ID && snapshotTask.Status == workflow.StatusDone {
			t.Fatalf("expected task to remain not-done after rejection: %#v", snapshotTask)
		}
	}
}

func TestAutoReviewOptOutSkipsReviewer(t *testing.T) {
	t.Parallel()

	client, repoDirectory, cleanup := newReviewTestServer(t, "VERDICT: REJECT\nirrelevant")
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var workspace daemon.Workspace
	if err := client.Call(ctx, "workspace.create", map[string]string{"directory": repoDirectory}, &workspace); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	autoReview := false
	var task workflow.Task
	if err := client.Call(ctx, "task.create", map[string]any{
		"workspace_id": workspace.ID,
		"title":        "no review needed",
		"auto_review":  &autoReview,
	}, &task); err != nil {
		t.Fatalf("create task: %v", err)
	}
	if task.AutoReview {
		t.Fatalf("expected auto_review to be false")
	}

	var done workflow.Task
	if err := client.Call(ctx, "task.setStatus", map[string]string{
		"task_id": task.ID,
		"status":  string(workflow.StatusDone),
	}, &done); err != nil {
		t.Fatalf("set status to done without review: %v", err)
	}
	if done.Status != workflow.StatusDone {
		t.Fatalf("unexpected status: %q", done.Status)
	}
}
