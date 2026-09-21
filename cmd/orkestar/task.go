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
	"github.com/martintrifunov/orkestar/internal/ipc"
	"github.com/martintrifunov/orkestar/internal/runtimepath"
	"github.com/martintrifunov/orkestar/internal/usage"
	"github.com/martintrifunov/orkestar/internal/workflow"
)

// runTemplate handles the template verbs, which sit beside tasks because that
// is what a template is: a set of them, declared in a file.
func runTemplate(paths runtimepath.Paths, args []string) error {
	if len(args) == 0 {
		return errTemplateUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	client := ipc.NewClient(paths.Socket)

	switch args[0] {
	case "list":
		if len(args) != 2 {
			return errTemplateUsage
		}
		var listed struct {
			Templates []workflow.Template `json:"templates"`
		}
		if err := client.Call(ctx, "template.list", map[string]any{"workspace_id": args[1]}, &listed); err != nil {
			return err
		}
		for _, template := range listed.Templates {
			fmt.Printf("%s\t%d tasks\t%s\n", template.Name, len(template.Tasks), template.Description)
		}
		return nil
	case "apply":
		if len(args) < 3 || len(args) > 4 {
			return errTemplateUsage
		}
		start := false
		if len(args) == 4 {
			if args[3] != "--start" {
				return errTemplateUsage
			}
			start = true
		}
		var applied daemon.AppliedTemplate
		if err := client.Call(ctx, "template.apply", map[string]any{
			"workspace_id": args[1], "name": args[2], "start": start,
		}, &applied); err != nil {
			return err
		}
		for _, task := range applied.Tasks {
			printTask(task)
		}
		for _, launched := range applied.Agents {
			fmt.Printf("started\t%s\t%s\n", launched.Adapter, launched.TaskID)
		}
		for _, waiting := range applied.Waiting {
			fmt.Printf("waiting\t%s\n", waiting)
		}
		return nil
	default:
		return errTemplateUsage
	}
}

var errTemplateUsage = errors.New(`usage:
  orkestar template list <workspace-id>
  orkestar template apply <workspace-id> <name> [--start]`)

func runTask(paths runtimepath.Paths, args []string) error {
	if len(args) == 0 {
		return errTaskUsage
	}

	switch args[0] {
	case "create":
		return taskCreate(paths, args[1:])
	case "list":
		return taskList(paths, args[1:])
	case "edit":
		return taskEdit(paths, args[1:])
	case "status":
		return taskStatus(paths, args[1:])
	case "assign":
		return taskAssign(paths, args[1:])
	case "auto-start":
		return taskAutoStart(paths, args[1:])
	case "budget":
		return taskBudget(paths, args[1:])
	case "import":
		return taskImport(paths, args[1:])
	case "pr":
		return taskPR(paths, args[1:])
	case "worktree":
		return taskWorktree(paths, args[1:])
	case "wait":
		return taskWait(paths, args[1:])
	case "diff":
		return taskDiff(paths, args[1:])
	case "watch":
		return taskWatch(paths, args[1:])
	default:
		return errTaskUsage
	}
}

var errTaskUsage = errors.New(`usage:
  orkestar task create <workspace-id> <title> [--depends-on id1,id2] [--no-review]
  orkestar task list [workspace-id]
  orkestar task edit <task-id> [--title=…] [--description=…] [--depends-on=id1,id2]
  orkestar task status <task-id> <pending|in_progress|done|cancelled>
  orkestar task assign <task-id> <agent-id>
  orkestar task auto-start <task-id> <agent> [--prompt=text] | orkestar task auto-start <task-id> --clear
  orkestar task budget <task-id> [--tokens=N] [--seconds=N] [--action=warn|stop] [--clear]
  orkestar task import <workspace-id> <issue-number-or-url>
  orkestar task pr <task-id>
  orkestar task worktree create <task-id> [branch]
  orkestar task worktree remove <task-id>
  orkestar task wait <task-id> [done|finished|startable] [--timeout=300]
  orkestar task diff <task-id>
  orkestar task watch`)

// taskWatch streams the task board, printing the whole board on each change.
// It is the subscription counterpart to `task wait`: a wait follows one task,
// and this follows every task until interrupted.
func taskWatch(paths runtimepath.Paths, args []string) error {
	if len(args) != 0 {
		return errTaskUsage
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	var initial struct {
		Tasks []workflow.Task `json:"tasks"`
	}
	stream, err := ipc.NewClient(paths.Socket).OpenStream(ctx, "task.attach", nil, &initial)
	if err != nil {
		return err
	}
	defer stream.Close()
	// Receive has no context, so closing the stream is what releases a blocked
	// read on Ctrl-C; otherwise the watch could only be killed with SIGKILL.
	go func() {
		<-ctx.Done()
		_ = stream.Close()
	}()
	for _, task := range initial.Tasks {
		printTask(task)
	}
	for {
		var event ipc.Event
		if err := stream.Receive(&event); err != nil {
			if ctx.Err() != nil || errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if event.Event != "task.updated" {
			continue
		}
		var payload struct {
			Tasks []workflow.Task `json:"tasks"`
		}
		if err := json.Unmarshal(event.Data, &payload); err != nil {
			return err
		}
		fmt.Println("--")
		for _, task := range payload.Tasks {
			printTask(task)
		}
	}
}

func taskCreate(paths runtimepath.Paths, args []string) error {
	if len(args) < 2 {
		return errTaskUsage
	}
	workspaceID := args[0]
	title := args[1]

	autoReview := true
	autoStart := false
	autoAgent := ""
	autoPrompt := ""
	var dependsOn []string
	for _, flag := range args[2:] {
		switch {
		case flag == "--no-review":
			autoReview = false
		case strings.HasPrefix(flag, "--depends-on="):
			value := strings.TrimPrefix(flag, "--depends-on=")
			if value != "" {
				dependsOn = strings.Split(value, ",")
			}
		case strings.HasPrefix(flag, "--auto-start="):
			autoStart = true
			autoAgent = strings.TrimPrefix(flag, "--auto-start=")
		case strings.HasPrefix(flag, "--auto-prompt="):
			autoPrompt = strings.TrimPrefix(flag, "--auto-prompt=")
		default:
			return fmt.Errorf("unknown flag %q\n\n%s", flag, errTaskUsage.Error())
		}
	}
	if autoStart && autoAgent == "" {
		return fmt.Errorf("--auto-start needs an adapter name\n\n%s", errTaskUsage.Error())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var task workflow.Task
	if err := ipc.NewClient(paths.Socket).Call(ctx, "task.create", map[string]any{
		"workspace_id": workspaceID,
		"title":        title,
		"depends_on":   dependsOn,
		"auto_review":  &autoReview,
		"auto_start":   autoStart,
		"auto_agent":   autoAgent,
		"auto_prompt":  autoPrompt,
	}, &task); err != nil {
		return err
	}
	printTask(task)
	return nil
}

// taskEdit changes a task after it was created. Only the flags given are
// sent, so editing a title cannot blank a description; --depends-on replaces
// the whole list, and an empty value clears it.
func taskEdit(paths runtimepath.Paths, args []string) error {
	if len(args) < 2 {
		return errTaskUsage
	}
	params := map[string]any{"task_id": args[0]}
	for _, flag := range args[1:] {
		switch {
		case strings.HasPrefix(flag, "--title="):
			params["title"] = strings.TrimPrefix(flag, "--title=")
		case strings.HasPrefix(flag, "--description="):
			params["description"] = strings.TrimPrefix(flag, "--description=")
		case strings.HasPrefix(flag, "--depends-on="):
			value := strings.TrimPrefix(flag, "--depends-on=")
			dependsOn := []string{}
			if value != "" {
				dependsOn = strings.Split(value, ",")
			}
			params["depends_on"] = dependsOn
		default:
			return fmt.Errorf("unknown flag %q\n\n%s", flag, errTaskUsage.Error())
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var task workflow.Task
	if err := ipc.NewClient(paths.Socket).Call(ctx, "task.update", params, &task); err != nil {
		return err
	}
	printTask(task)
	return nil
}

func taskList(paths runtimepath.Paths, args []string) error {
	if len(args) > 1 {
		return errTaskUsage
	}
	var workspaceFilter string
	if len(args) == 1 {
		workspaceFilter = args[0]
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var snapshot daemon.Snapshot
	if err := ipc.NewClient(paths.Socket).Call(ctx, "system.snapshot", nil, &snapshot); err != nil {
		return err
	}
	for _, task := range snapshot.Tasks {
		if workspaceFilter != "" && task.WorkspaceID != workspaceFilter {
			continue
		}
		printTask(task)
	}
	return nil
}

func taskStatus(paths runtimepath.Paths, args []string) error {
	if len(args) != 2 {
		return errTaskUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var task workflow.Task
	if err := ipc.NewClient(paths.Socket).Call(ctx, "task.setStatus", map[string]string{
		"task_id": args[0],
		"status":  args[1],
	}, &task); err != nil {
		return err
	}
	printTask(task)
	return nil
}

func taskAssign(paths runtimepath.Paths, args []string) error {
	if len(args) != 2 {
		return errTaskUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var task workflow.Task
	if err := ipc.NewClient(paths.Socket).Call(ctx, "task.assign", map[string]string{
		"task_id":  args[0],
		"agent_id": args[1],
	}, &task); err != nil {
		return err
	}
	printTask(task)
	return nil
}

// taskAutoStart declares which agent to launch once a task's dependencies are
// done, or clears a declaration that has not fired yet.
func taskAutoStart(paths runtimepath.Paths, args []string) error {
	if len(args) < 1 {
		return errTaskUsage
	}
	taskID := args[0]
	agent := ""
	prompt := ""
	clear := false
	for _, argument := range args[1:] {
		switch {
		case argument == "--clear":
			clear = true
		case strings.HasPrefix(argument, "--prompt="):
			prompt = strings.TrimPrefix(argument, "--prompt=")
		case strings.HasPrefix(argument, "--"):
			return fmt.Errorf("unknown flag %q\n\n%s", argument, errTaskUsage.Error())
		default:
			if agent != "" {
				return errTaskUsage
			}
			agent = argument
		}
	}
	if clear {
		agent, prompt = "", ""
	}
	if !clear && agent == "" {
		return errTaskUsage
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var task workflow.Task
	if err := ipc.NewClient(paths.Socket).Call(ctx, "task.setAutoStart", map[string]any{
		"task_id": taskID, "agent": agent, "prompt": prompt,
	}, &task); err != nil {
		return err
	}
	printTask(task)
	return nil
}

// taskBudget records what an agent working a task may spend. A token budget
// is checked against what the provider's transcript reports; a time budget
// bounds one session and works for every adapter.
func taskBudget(paths runtimepath.Paths, args []string) error {
	if len(args) < 1 {
		return errTaskUsage
	}
	taskID := args[0]
	var tokens, seconds int64
	action := ""
	clear := false
	for _, argument := range args[1:] {
		switch {
		case argument == "--clear":
			clear = true
		case strings.HasPrefix(argument, "--tokens="):
			value, err := strconv.ParseInt(strings.TrimPrefix(argument, "--tokens="), 10, 64)
			if err != nil || value < 0 {
				return fmt.Errorf("--tokens must be a non-negative integer")
			}
			tokens = value
		case strings.HasPrefix(argument, "--seconds="):
			value, err := strconv.ParseInt(strings.TrimPrefix(argument, "--seconds="), 10, 64)
			if err != nil || value < 0 {
				return fmt.Errorf("--seconds must be a non-negative integer")
			}
			seconds = value
		case strings.HasPrefix(argument, "--action="):
			action = strings.TrimPrefix(argument, "--action=")
			if action != workflow.BudgetActionWarn && action != workflow.BudgetActionStop {
				return fmt.Errorf("--action must be warn or stop")
			}
		default:
			return fmt.Errorf("unknown flag %q\n\n%s", argument, errTaskUsage.Error())
		}
	}
	if clear {
		tokens, seconds, action = 0, 0, ""
	}
	if !clear && tokens == 0 && seconds == 0 {
		return fmt.Errorf("a budget needs --tokens or --seconds\n\n%s", errTaskUsage.Error())
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var task workflow.Task
	if err := ipc.NewClient(paths.Socket).Call(ctx, "task.setBudget", map[string]any{
		"task_id": taskID, "tokens": tokens, "seconds": seconds, "action": action,
	}, &task); err != nil {
		return err
	}
	printTask(task)
	return nil
}

// taskImport creates a task from a GitHub issue, through gh's own
// authentication. It talks to the network, so it gets a longer deadline than
// the local verbs.
func taskImport(paths runtimepath.Paths, args []string) error {
	if len(args) != 2 {
		return errTaskUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var imported daemon.GitHubImport
	if err := ipc.NewClient(paths.Socket).Call(ctx, "task.githubImport", map[string]string{
		"workspace_id": args[0], "reference": args[1],
	}, &imported); err != nil {
		return err
	}
	fmt.Printf("%s\t%s\t%s\n", imported.Task.ID, imported.URL, imported.Task.Title)
	return nil
}

// taskPR opens a pull request for a finished task's worktree branch and
// records its URL as an artifact.
func taskPR(paths runtimepath.Paths, args []string) error {
	if len(args) != 1 {
		return errTaskUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var artifact workflow.Artifact
	if err := ipc.NewClient(paths.Socket).Call(ctx, "task.githubPR", map[string]string{
		"task_id": args[0],
	}, &artifact); err != nil {
		return err
	}
	fmt.Printf("%s\t%s\n", artifact.Path, artifact.Label)
	return nil
}

func taskWorktree(paths runtimepath.Paths, args []string) error {
	if len(args) < 2 {
		return errTaskUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var task workflow.Task

	switch args[0] {
	case "create":
		if len(args) > 3 {
			return errTaskUsage
		}
		params := map[string]string{"task_id": args[1]}
		if len(args) == 3 {
			params["branch"] = args[2]
		}
		if err := ipc.NewClient(paths.Socket).Call(ctx, "task.createWorktree", params, &task); err != nil {
			return err
		}
	case "remove":
		if len(args) != 2 {
			return errTaskUsage
		}
		if err := ipc.NewClient(paths.Socket).Call(ctx, "task.removeWorktree", map[string]string{
			"task_id": args[1],
		}, &task); err != nil {
			return err
		}
	default:
		return errTaskUsage
	}
	printTask(task)
	return nil
}

// taskWait blocks until a task reaches a state. It exists for scripts and
// agents: everything else the daemon offers answers immediately, so following
// another agent's work otherwise means asking in a loop.
func taskWait(paths runtimepath.Paths, args []string) error {
	if len(args) < 1 || len(args) > 3 {
		return errTaskUsage
	}
	until, timeout := "finished", 300
	for _, argument := range args[1:] {
		if value, ok := strings.CutPrefix(argument, "--timeout="); ok {
			parsed, err := strconv.Atoi(value)
			if err != nil {
				return fmt.Errorf("invalid timeout %q", value)
			}
			// Zero means the daemon's default, which is longer than this
			// command's own deadline would be if it took the number at face
			// value: the wait would then die on the connection rather than on
			// the timeout it was given.
			if parsed <= 0 {
				parsed = 300
			}
			timeout = parsed
			continue
		}
		until = argument
	}

	// The connection deadline comes from this context, so it has to outlast
	// the wait the daemon was asked for.
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout+15)*time.Second)
	defer cancel()
	var task workflow.Task
	if err := ipc.NewClient(paths.Socket).Call(ctx, "task.wait", map[string]any{
		"task_id": args[0], "until": until, "timeout_seconds": timeout,
	}, &task); err != nil {
		return err
	}
	printTask(task)
	return nil
}

func taskDiff(paths runtimepath.Paths, args []string) error {
	if len(args) != 1 {
		return errTaskUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var diff daemon.TaskDiff
	if err := ipc.NewClient(paths.Socket).Call(ctx, "task.diff", map[string]string{
		"task_id": args[0],
	}, &diff); err != nil {
		return err
	}
	for _, file := range diff.Files {
		fmt.Printf("%s %s\n", file.Status, file.Path)
	}
	if diff.Diff != "" {
		fmt.Println()
		fmt.Print(diff.Diff)
	}
	return nil
}

func printTask(task workflow.Task) {
	worktree := "-"
	if task.WorktreeBranch != "" {
		worktree = task.WorktreeBranch
	}
	assignee := "-"
	if task.AssigneeAgentID != "" {
		assignee = task.AssigneeAgentID
	}
	auto := "-"
	switch {
	case task.AutoStartError != "":
		auto = "!" + task.AutoStartError
	case task.AutoStart:
		auto = task.AutoAgent
	}
	budget := "-"
	switch {
	case task.TokenBudget > 0:
		budget = usage.FormatTokens(task.TokenBudget)
		if task.BudgetAction == workflow.BudgetActionStop {
			budget += "/stop"
		}
	case task.TimeBudgetSeconds > 0:
		budget = (time.Duration(task.TimeBudgetSeconds) * time.Second).String()
		if task.BudgetAction == workflow.BudgetActionStop {
			budget += "/stop"
		}
	}
	fmt.Printf("%s\t%-11s\t%v\t%s\t%s\t%s\t%s\t%s\n", task.ID, task.Status, task.AutoReview, worktree, assignee, auto, budget, task.Title)
}
