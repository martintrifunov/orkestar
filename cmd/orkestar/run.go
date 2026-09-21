package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/martintrifunov/orkestar/internal/daemon"
	"github.com/martintrifunov/orkestar/internal/daemonclient"
	"github.com/martintrifunov/orkestar/internal/ipc"
	"github.com/martintrifunov/orkestar/internal/runtimepath"
	"github.com/martintrifunov/orkestar/internal/workflow"
)

var errRunUsage = errors.New(`usage:
  orkestar run [--workspace=DIR] [--task=<task-id> | --template=NAME]
               [--agent=ADAPTER] [--prompt=TEXT] [--title=TITLE]
               [--timeout=600] [--no-review]

Runs one piece of work start to finish without a terminal: creates or reuses a
workspace, creates a task or applies a template, starts an agent on it, waits
for every task to finish, and prints a JSON summary. Exits non-zero when the
work did not finish done`)

// runOptions is what the command line asked for.
type runOptions struct {
	workspace  string
	taskID     string
	template   string
	agent      string
	prompt     string
	title      string
	timeout    int
	autoReview bool
}

func parseRunArgs(args []string) (runOptions, error) {
	options := runOptions{timeout: 600, autoReview: true}
	for _, argument := range args {
		switch {
		case strings.HasPrefix(argument, "--workspace="):
			options.workspace = strings.TrimPrefix(argument, "--workspace=")
		case strings.HasPrefix(argument, "--task="):
			options.taskID = strings.TrimPrefix(argument, "--task=")
		case strings.HasPrefix(argument, "--template="):
			options.template = strings.TrimPrefix(argument, "--template=")
		case strings.HasPrefix(argument, "--agent="):
			options.agent = strings.TrimPrefix(argument, "--agent=")
		case strings.HasPrefix(argument, "--prompt="):
			options.prompt = strings.TrimPrefix(argument, "--prompt=")
		case strings.HasPrefix(argument, "--title="):
			options.title = strings.TrimPrefix(argument, "--title=")
		case strings.HasPrefix(argument, "--timeout="):
			seconds, err := strconv.Atoi(strings.TrimPrefix(argument, "--timeout="))
			if err != nil || seconds <= 0 {
				return runOptions{}, fmt.Errorf("--timeout must be a positive number of seconds")
			}
			options.timeout = seconds
		case argument == "--no-review":
			options.autoReview = false
		default:
			return runOptions{}, fmt.Errorf("unknown flag %q\n\n%s", argument, errRunUsage.Error())
		}
	}
	if options.taskID != "" && options.template != "" {
		return runOptions{}, fmt.Errorf("--task and --template are different runs; pick one\n\n%s", errRunUsage.Error())
	}
	return options, nil
}

// RunTaskSummary is one task in a headless run's result.
type RunTaskSummary struct {
	ID     string          `json:"id"`
	Title  string          `json:"title"`
	Status workflow.Status `json:"status"`
}

// RunSummary is what `orkestar run` prints: enough for CI to decide what
// happened without parsing prose.
type RunSummary struct {
	WorkspaceID string              `json:"workspace_id"`
	Status      string              `json:"status"`
	Tasks       []RunTaskSummary    `json:"tasks"`
	Agents      []string            `json:"agents,omitempty"`
	Artifacts   []workflow.Artifact `json:"artifacts,omitempty"`
}

func runRun(paths runtimepath.Paths, args []string) error {
	return runPipeline(paths, args, os.Stdout)
}

// runPipeline is runRun with its output injectable, so a test can read the
// summary without capturing the process's stdout.
func runPipeline(paths runtimepath.Paths, args []string, stdout io.Writer) error {
	options, err := parseRunArgs(args)
	if err != nil {
		return err
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := daemonclient.Ensure(ctx, paths); err != nil {
		return err
	}
	client := ipc.NewClient(paths.Socket)

	directory := options.workspace
	if directory == "" {
		directory, err = os.Getwd()
		if err != nil {
			return fmt.Errorf("resolve working directory: %w", err)
		}
	}
	var workspace daemon.Workspace
	if err := client.Call(ctx, "workspace.create", map[string]string{"directory": directory}, &workspace); err != nil {
		return err
	}

	var taskIDs []string
	switch {
	case options.taskID != "":
		taskIDs = []string{options.taskID}
	case options.template != "":
		applied, err := applyRunTemplate(ctx, client, workspace.ID, options.template)
		if err != nil {
			return err
		}
		for _, task := range applied.Tasks {
			taskIDs = append(taskIDs, task.ID)
		}
	default:
		title := options.title
		if title == "" {
			title = runTitle(options.prompt)
		}
		var task workflow.Task
		if err := client.Call(ctx, "task.create", map[string]any{
			"workspace_id": workspace.ID, "title": title, "description": options.prompt,
			"auto_review": options.autoReview,
		}, &task); err != nil {
			return err
		}
		taskIDs = []string{task.ID}
	}

	agent, err := resolveRunAdapter(ctx, client, options.agent)
	if err != nil {
		return err
	}
	// A task already carrying an assignee was started by someone else (an
	// auto_start, or a person) and is not started again here.
	for _, taskID := range taskIDs {
		if err := startRunTask(ctx, client, taskID, agent, options.prompt); err != nil {
			return err
		}
	}

	if err := waitForRun(ctx, client, taskIDs, options.timeout); err != nil {
		// The summary is still printed so a caller can see what finished
		// before the deadline; the error is what makes the exit code non-zero.
		summary, summaryErr := runSummary(ctx, client, workspace.ID, taskIDs)
		if summaryErr == nil {
			_ = json.NewEncoder(stdout).Encode(summary)
		}
		return err
	}
	summary, err := runSummary(ctx, client, workspace.ID, taskIDs)
	if err != nil {
		return err
	}
	if err := json.NewEncoder(stdout).Encode(summary); err != nil {
		return err
	}
	if summary.Status != "done" {
		return fmt.Errorf("run finished %s", summary.Status)
	}
	return nil
}

func applyRunTemplate(ctx context.Context, client *ipc.Client, workspaceID, name string) (daemon.AppliedTemplate, error) {
	var applied daemon.AppliedTemplate
	if err := client.Call(ctx, "template.apply", map[string]any{
		"workspace_id": workspaceID, "name": name, "start": true,
	}, &applied); err != nil {
		return daemon.AppliedTemplate{}, err
	}
	if applied.Failed != "" {
		return daemon.AppliedTemplate{}, fmt.Errorf("apply template: %s", applied.Failed)
	}
	if len(applied.Waiting) > 0 {
		// Nothing headless will start these: a waiting task is one whose
		// dependencies have not finished and whose template did not ask for
		// an automatic start.
		return daemon.AppliedTemplate{}, fmt.Errorf("template has %d task(s) waiting to be started by hand; mark them auto_start or run them with an orchestrator", len(applied.Waiting))
	}
	return applied, nil
}

// resolveRunAdapter picks the adapter to launch: the named one, or the only
// registered one when there is no choice to make.
func resolveRunAdapter(ctx context.Context, client *ipc.Client, name string) (string, error) {
	if name != "" {
		return name, nil
	}
	var snapshot daemon.Snapshot
	if err := client.Call(ctx, "system.snapshot", nil, &snapshot); err != nil {
		return "", err
	}
	if len(snapshot.Adapters) == 1 {
		return snapshot.Adapters[0].Name, nil
	}
	names := make([]string, 0, len(snapshot.Adapters))
	for _, adapter := range snapshot.Adapters {
		names = append(names, adapter.Name)
	}
	return "", fmt.Errorf("more than one adapter is registered (%s); name one with --agent", strings.Join(names, ", "))
}

// startRunTask launches an agent for a task that does not already have one.
func startRunTask(ctx context.Context, client *ipc.Client, taskID, adapter, prompt string) error {
	var snapshot daemon.Snapshot
	if err := client.Call(ctx, "system.snapshot", nil, &snapshot); err != nil {
		return err
	}
	for _, task := range snapshot.Tasks {
		if task.ID != taskID {
			continue
		}
		if task.AssigneeAgentID != "" {
			return nil
		}
		if task.Status != workflow.StatusPending {
			return nil
		}
		if prompt == "" {
			prompt = "You have been assigned this task: " + task.Title
			if task.Description != "" {
				prompt += "\n\n" + task.Description
			}
		}
		var launched daemon.Agent
		return client.Call(ctx, "agent.launch", map[string]any{
			"workspace_id": task.WorkspaceID, "adapter": adapter,
			"mode": "interactive", "task_id": task.ID, "prompt": prompt,
		}, &launched)
	}
	return fmt.Errorf("task %q does not exist", taskID)
}

// waitForRun blocks until every task is finished, bounded by the deadline.
// A cancelled task fails the run rather than being treated as a completion.
func waitForRun(ctx context.Context, client *ipc.Client, taskIDs []string, timeout int) error {
	deadline := time.Now().Add(time.Duration(timeout) * time.Second)
	for _, taskID := range taskIDs {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return fmt.Errorf("timed out after %ds waiting for %s", timeout, taskID)
		}
		seconds := int(remaining / time.Second)
		if seconds < 1 {
			seconds = 1
		}
		if seconds > int(time.Hour/time.Second) {
			seconds = int(time.Hour / time.Second)
		}
		var task workflow.Task
		if err := client.Call(ctx, "task.wait", map[string]any{
			"task_id": taskID, "until": string(workflow.ConditionFinished), "timeout_seconds": seconds,
		}, &task); err != nil {
			return err
		}
	}
	return nil
}

// runSummary reads the final board state for the tasks the run touched.
func runSummary(ctx context.Context, client *ipc.Client, workspaceID string, taskIDs []string) (RunSummary, error) {
	var snapshot daemon.Snapshot
	if err := client.Call(ctx, "system.snapshot", nil, &snapshot); err != nil {
		return RunSummary{}, err
	}
	wanted := make(map[string]bool, len(taskIDs))
	for _, taskID := range taskIDs {
		wanted[taskID] = true
	}
	summary := RunSummary{WorkspaceID: workspaceID, Status: "done"}
	for _, task := range snapshot.Tasks {
		if !wanted[task.ID] {
			continue
		}
		summary.Tasks = append(summary.Tasks, RunTaskSummary{ID: task.ID, Title: task.Title, Status: task.Status})
		switch task.Status {
		case workflow.StatusDone:
		case workflow.StatusCancelled:
			summary.Status = "cancelled"
		default:
			summary.Status = "failed"
		}
	}
	for _, agent := range snapshot.Agents {
		if wanted[agent.TaskID] {
			summary.Agents = append(summary.Agents, agent.ID)
		}
	}
	for _, artifact := range snapshot.Artifacts {
		if wanted[artifact.TaskID] {
			summary.Artifacts = append(summary.Artifacts, artifact)
		}
	}
	if len(summary.Tasks) == 0 {
		summary.Status = "failed"
	}
	return summary, nil
}

// runTitle names a task created from a prompt: the first line, bounded, so a
// long prompt does not become a title nobody can read in a sidebar.
func runTitle(prompt string) string {
	if prompt == "" {
		return "run"
	}
	title := strings.TrimSpace(strings.SplitN(prompt, "\n", 2)[0])
	if title == "" {
		return "run"
	}
	if len(title) > 80 {
		title = title[:80]
	}
	return title
}
