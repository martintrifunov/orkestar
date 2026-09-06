package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/martintrifunov/orkestar/internal/daemon"
	"github.com/martintrifunov/orkestar/internal/ipc"
	"github.com/martintrifunov/orkestar/internal/runtimepath"
	"github.com/martintrifunov/orkestar/internal/workflow"
)

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
	case "worktree":
		return taskWorktree(paths, args[1:])
	case "diff":
		return taskDiff(paths, args[1:])
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
  orkestar task worktree create <task-id> [branch]
  orkestar task worktree remove <task-id>
  orkestar task diff <task-id>`)

func taskCreate(paths runtimepath.Paths, args []string) error {
	if len(args) < 2 {
		return errTaskUsage
	}
	workspaceID := args[0]
	title := args[1]

	autoReview := true
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
		default:
			return fmt.Errorf("unknown flag %q\n\n%s", flag, errTaskUsage.Error())
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var task workflow.Task
	if err := ipc.NewClient(paths.Socket).Call(ctx, "task.create", map[string]any{
		"workspace_id": workspaceID,
		"title":        title,
		"depends_on":   dependsOn,
		"auto_review":  &autoReview,
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
	fmt.Printf("%s\t%-11s\t%v\t%s\t%s\t%s\n", task.ID, task.Status, task.AutoReview, worktree, assignee, task.Title)
}
