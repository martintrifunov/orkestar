package git_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/martintrifunov/orkestar/internal/git"
)

func initRepo(t *testing.T) string {
	t.Helper()

	directory := t.TempDir()
	run := func(args ...string) {
		command := exec.Command("git", append([]string{"-C", directory}, args...)...)
		command.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
		)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	run("init", "-b", "main")
	if err := os.WriteFile(filepath.Join(directory, "README.md"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write README: %v", err)
	}
	run("add", "README.md")
	run("commit", "-m", "initial commit")
	return directory
}

func TestAddWorktreeCreatesNewBranch(t *testing.T) {
	t.Parallel()

	repoDir := initRepo(t)
	worktreeDir := filepath.Join(t.TempDir(), "task-1")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := git.AddWorktree(ctx, repoDir, worktreeDir, "task/one"); err != nil {
		t.Fatalf("add worktree: %v", err)
	}
	if _, err := os.Stat(filepath.Join(worktreeDir, "README.md")); err != nil {
		t.Fatalf("expected worktree checkout: %v", err)
	}

	worktrees, err := git.ListWorktrees(ctx, repoDir)
	if err != nil {
		t.Fatalf("list worktrees: %v", err)
	}
	found := false
	for _, worktree := range worktrees {
		if worktree.Branch == "task/one" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected to find branch task/one in %#v", worktrees)
	}
}

func TestAddWorktreeReusesExistingBranch(t *testing.T) {
	t.Parallel()

	repoDir := initRepo(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	firstPath := filepath.Join(t.TempDir(), "first")
	if err := git.AddWorktree(ctx, repoDir, firstPath, "shared"); err != nil {
		t.Fatalf("add first worktree: %v", err)
	}
	if err := git.RemoveWorktree(ctx, repoDir, firstPath); err != nil {
		t.Fatalf("remove first worktree: %v", err)
	}

	secondPath := filepath.Join(t.TempDir(), "second")
	if err := git.AddWorktree(ctx, repoDir, secondPath, "shared"); err != nil {
		t.Fatalf("add second worktree reusing branch: %v", err)
	}
}

func TestChangedFilesAndDiff(t *testing.T) {
	t.Parallel()

	repoDir := initRepo(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if files, err := git.ChangedFiles(ctx, repoDir); err != nil || len(files) != 0 {
		t.Fatalf("expected no changed files on a clean repo, got %#v, err %v", files, err)
	}

	if err := os.WriteFile(filepath.Join(repoDir, "README.md"), []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatalf("modify README: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoDir, "NEW.md"), []byte("new file\n"), 0o644); err != nil {
		t.Fatalf("write NEW.md: %v", err)
	}

	files, err := git.ChangedFiles(ctx, repoDir)
	if err != nil {
		t.Fatalf("changed files: %v", err)
	}
	paths := make(map[string]bool)
	for _, file := range files {
		paths[file.Path] = true
	}
	if !paths["README.md"] || !paths["NEW.md"] {
		t.Fatalf("expected README.md and NEW.md in changed files, got %#v", files)
	}

	diff, err := git.Diff(ctx, repoDir)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if !strings.Contains(diff, "README.md") || !strings.Contains(diff, "+world") {
		t.Fatalf("expected diff to mention README.md changes, got %q", diff)
	}
}

func TestRemoveWorktree(t *testing.T) {
	t.Parallel()

	repoDir := initRepo(t)
	worktreeDir := filepath.Join(t.TempDir(), "task-2")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := git.AddWorktree(ctx, repoDir, worktreeDir, "task/two"); err != nil {
		t.Fatalf("add worktree: %v", err)
	}
	if err := git.RemoveWorktree(ctx, repoDir, worktreeDir); err != nil {
		t.Fatalf("remove worktree: %v", err)
	}
	if _, err := os.Stat(worktreeDir); !os.IsNotExist(err) {
		t.Fatalf("expected worktree directory to be removed, stat err: %v", err)
	}

	worktrees, err := git.ListWorktrees(ctx, repoDir)
	if err != nil {
		t.Fatalf("list worktrees: %v", err)
	}
	for _, worktree := range worktrees {
		if worktree.Path == worktreeDir {
			t.Fatalf("expected worktree to be removed from list: %#v", worktrees)
		}
	}
}

func TestDiffIncludesUntrackedNamesWithoutQuotingThemInStatus(t *testing.T) {
	root := initRepo(t)
	name := " new file é.txt"
	if err := os.WriteFile(filepath.Join(root, name), []byte("untracked content\n"), 0644); err != nil {
		t.Fatal(err)
	}
	changed, err := git.ChangedFiles(t.Context(), root)
	if err != nil || len(changed) != 1 || changed[0].Path != name {
		t.Fatalf("%+v %v", changed, err)
	}
	diff, err := git.Diff(t.Context(), root)
	if err != nil || !strings.Contains(diff, "+untracked content") {
		t.Fatalf("%q %v", diff, err)
	}
}
