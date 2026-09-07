// Package git wraps the `git worktree` commands Orkestar uses to give a
// task its own working directory and branch alongside a workspace's
// primary checkout.
package git

import (
	"bytes"
	"context"
	"fmt"
	"github.com/martintrifunov/orkestar/internal/files"
	"os/exec"
	"strconv"
	"strings"
	"time"
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
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "git", args...)
	command.WaitDelay = time.Second
	var output boundedOutput
	command.Stdout = &output
	command.Stderr = &output
	err := command.Run()
	if output.overflow {
		return "", fmt.Errorf("git output exceeds 2 MiB")
	}
	return output.String(), err
}

// ChangedFile is one entry from `git status --porcelain`.
type ChangedFile struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}

// ChangedFiles lists files with uncommitted changes (staged, unstaged, or
// untracked) in repoDir.
func ChangedFiles(ctx context.Context, repoDir string) ([]ChangedFile, error) {
	output, err := runGit(ctx, "-C", repoDir, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return nil, fmt.Errorf("status %q: %w: %s", repoDir, err, output)
	}
	if output == "" {
		return nil, nil
	}

	var files []ChangedFile
	parts := strings.Split(output, "\x00")
	for i := 0; i < len(parts); i++ {
		line := parts[i]
		if len(line) < 4 {
			continue
		}
		files = append(files, ChangedFile{Status: line[:2], Path: line[3:]})
		if strings.ContainsAny(line[:2], "RC") {
			i++
		}
	}
	return files, nil
}

// Diff returns the unified diff of uncommitted changes in repoDir,
// covering staged, unstaged and untracked text changes against HEAD. It does not
// omit untracked text files; they are represented as additions.
func Diff(ctx context.Context, repoDir string) (string, error) {
	base := "HEAD"
	if _, err := runGit(ctx, "-C", repoDir, "rev-parse", "--verify", "HEAD"); err != nil {
		base = "--cached"
	}
	output, err := runGit(ctx, "-C", repoDir, "diff", "--no-ext-diff", "--no-textconv", "--no-color", base, "--")
	if err != nil {
		return "", fmt.Errorf("diff %q: %w: %s", repoDir, err, output)
	}
	changed, err := ChangedFiles(ctx, repoDir)
	if err != nil {
		return "", err
	}
	var diff boundedOutput
	_, _ = diff.Write([]byte(output))
	for _, file := range changed {
		if file.Status != "??" {
			continue
		}
		doc, err := files.Open(repoDir, file.Path)
		if err != nil {
			return "", fmt.Errorf("diff untracked file %q: %w", file.Path, err)
		}
		lines := strings.Split(strings.TrimSuffix(doc.Text, "\n"), "\n")
		if doc.Text == "" {
			lines = nil
		}
		fmt.Fprintf(&diff, "diff --git %s %s\nnew file mode 100644\n--- /dev/null\n+++ %s\n@@ -0,0 +1,%d @@\n", strconv.Quote("a/"+file.Path), strconv.Quote("b/"+file.Path), strconv.Quote("b/"+file.Path), len(lines))
		for _, line := range lines {
			fmt.Fprintf(&diff, "+%s\n", line)
		}
		if doc.Text != "" && !strings.HasSuffix(doc.Text, "\n") {
			fmt.Fprintln(&diff, "\\ No newline at end of file")
		}
	}
	if diff.overflow {
		return "", fmt.Errorf("git diff exceeds 2 MiB")
	}
	return diff.String(), nil
}

type boundedOutput struct {
	bytes.Buffer
	overflow bool
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	n := len(p)
	remaining := 2*1024*1024 - b.Len()
	if n > remaining {
		b.overflow = true
		p = p[:remaining]
	}
	_, _ = b.Buffer.Write(p)
	return n, nil
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
