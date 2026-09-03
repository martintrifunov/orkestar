// Package git wraps the `git worktree` commands Orkestar uses to give a
// task its own working directory and branch alongside a workspace's
// primary checkout.
package git

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Worktree describes one entry from `git worktree list`.
type Worktree struct {
	Path   string
	Head   string
	Branch string
}

// AddWorktree creates a new worktree at path, associated with branch. If
// branch does not already exist in repoDir, it is created from HEAD.
func AddWorktree(ctx context.Context, repoDir, path, branch string) error {
	if branch == "" {
		return fmt.Errorf("add worktree: branch is required")
	}

	args := []string{"-C", repoDir, "worktree", "add"}
	if !branchExists(ctx, repoDir, branch) {
		args = append(args, "-b", branch, path)
	} else {
		args = append(args, path, branch)
	}

	if output, err := runGit(ctx, args...); err != nil {
		return fmt.Errorf("add worktree at %q for branch %q: %w: %s", path, branch, err, output)
	}
	return nil
}

// RemoveWorktree removes a worktree previously created with AddWorktree.
// It refuses to remove a worktree with uncommitted changes; the caller
// must resolve or discard those first.
func RemoveWorktree(ctx context.Context, repoDir, path string) error {
	if output, err := runGit(ctx, "-C", repoDir, "worktree", "remove", path); err != nil {
		return fmt.Errorf("remove worktree at %q: %w: %s", path, err, output)
	}
	return nil
}

// ListWorktrees returns every worktree registered against repoDir.
func ListWorktrees(ctx context.Context, repoDir string) ([]Worktree, error) {
	output, err := runGit(ctx, "-C", repoDir, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, fmt.Errorf("list worktrees in %q: %w: %s", repoDir, err, output)
	}
	return parseWorktreeList(output), nil
}

func branchExists(ctx context.Context, repoDir, branch string) bool {
	_, err := runGit(ctx, "-C", repoDir, "show-ref", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}

func runGit(ctx context.Context, args ...string) (string, error) {
	command := exec.CommandContext(ctx, "git", args...)
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	err := command.Run()
	return strings.TrimRight(output.String(), "\n"), err
}

// ChangedFile is one entry from `git status --porcelain`.
type ChangedFile struct {
	Path   string
	Status string
}

// ChangedFiles lists files with uncommitted changes (staged, unstaged, or
// untracked) in repoDir.
func ChangedFiles(ctx context.Context, repoDir string) ([]ChangedFile, error) {
	output, err := runGit(ctx, "-C", repoDir, "status", "--porcelain")
	if err != nil {
		return nil, fmt.Errorf("status %q: %w: %s", repoDir, err, output)
	}
	if output == "" {
		return nil, nil
	}

	var files []ChangedFile
	for _, line := range strings.Split(output, "\n") {
		if len(line) < 4 {
			continue
		}
		files = append(files, ChangedFile{Status: line[:2], Path: strings.TrimSpace(line[3:])})
	}
	return files, nil
}

// Diff returns the unified diff of uncommitted changes in repoDir,
// covering both staged and unstaged changes against HEAD. It does not
// include untracked files; use ChangedFiles to see those.
func Diff(ctx context.Context, repoDir string) (string, error) {
	output, err := runGit(ctx, "-C", repoDir, "diff", "--no-color", "HEAD")
	if err != nil {
		return "", fmt.Errorf("diff %q: %w: %s", repoDir, err, output)
	}
	return output, nil
}

func parseWorktreeList(output string) []Worktree {
	var worktrees []Worktree
	var current Worktree
	flush := func() {
		if current.Path != "" {
			worktrees = append(worktrees, current)
		}
		current = Worktree{}
	}

	for _, line := range strings.Split(output, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			flush()
			current.Path = strings.TrimPrefix(line, "worktree ")
		case strings.HasPrefix(line, "HEAD "):
			current.Head = strings.TrimPrefix(line, "HEAD ")
		case strings.HasPrefix(line, "branch "):
			current.Branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		}
	}
	flush()
	return worktrees
}
