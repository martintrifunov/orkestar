// Package github runs the gh CLI. The CLI is the authentication boundary:
// Orkestar stores no token and never reads one, consistent with the rest of
// the product's credential rules. gh missing or unauthenticated is reported
// as an error, not worked around.
package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// runTimeout bounds one gh invocation. gh talks to a network, so it gets
// longer than a git call but still a ceiling.
const runTimeout = 30 * time.Second

// maxOutput bounds what is read from gh, the way git output is bounded.
const maxOutput = 2 << 20

// Run executes gh in a directory and returns its stdout. stderr is folded
// into the error, where it is diagnostics.
func Run(ctx context.Context, directory string, args ...string) (string, error) {
	if _, err := exec.LookPath("gh"); err != nil {
		return "", errors.New("the GitHub CLI (gh) is not installed; install it and authenticate with `gh auth login`")
	}
	ctx, cancel := context.WithTimeout(ctx, runTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, "gh", args...)
	command.Dir = directory
	command.WaitDelay = time.Second

	var stdout, stderr boundedOutput
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	if stdout.overflow {
		return "", fmt.Errorf("gh output exceeds 2 MiB")
	}
	if err != nil {
		if message := strings.TrimSpace(stderr.String()); message != "" {
			return stdout.String(), fmt.Errorf("gh %s: %w: %s", args[0], err, message)
		}
		return stdout.String(), fmt.Errorf("gh %s: %w", args[0], err)
	}
	return stdout.String(), nil
}

// Issue is the part of an issue Orkestar imports.
type Issue struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	URL    string `json:"url"`
}

// ViewIssue reads one issue by number or URL.
func ViewIssue(ctx context.Context, directory, reference string) (Issue, error) {
	output, err := Run(ctx, directory, "issue", "view", reference, "--json", "number,title,body,url")
	if err != nil {
		return Issue{}, err
	}
	var issue Issue
	if err := json.Unmarshal([]byte(output), &issue); err != nil {
		return Issue{}, fmt.Errorf("parse gh issue output: %w", err)
	}
	if issue.Title == "" {
		return Issue{}, errors.New("gh returned an issue with no title")
	}
	return issue, nil
}

// CreatePullRequest opens a pull request for a branch and returns its URL.
// gh prints the URL on stdout.
func CreatePullRequest(ctx context.Context, directory, title, body, branch string) (string, error) {
	output, err := Run(ctx, directory, "pr", "create", "--title", title, "--body", body, "--head", branch)
	if err != nil {
		return "", err
	}
	url := ""
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "http") {
			url = strings.TrimSpace(line)
		}
	}
	if url == "" {
		return "", fmt.Errorf("gh did not report a pull request URL: %s", strings.TrimSpace(output))
	}
	return url, nil
}

// boundedOutput caps how much of a stream is kept. It records the overflow
// rather than growing without limit.
type boundedOutput struct {
	buffer   strings.Builder
	overflow bool
}

func (b *boundedOutput) Write(data []byte) (int, error) {
	if b.buffer.Len()+len(data) > maxOutput {
		room := maxOutput - b.buffer.Len()
		if room > 0 {
			b.buffer.Write(data[:room])
		}
		b.overflow = true
		return len(data), nil
	}
	b.buffer.Write(data)
	return len(data), nil
}

func (b *boundedOutput) String() string { return b.buffer.String() }
