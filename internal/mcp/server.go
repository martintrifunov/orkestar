// Package mcp exposes Orkestar's daemon capabilities (workspaces, tasks,
// resource leases, artifacts) as MCP tools, so any MCP-capable agent can
// call them directly rather than only through the orkestar CLI or IPC. It
// is a thin translation layer: every tool forwards to the same daemon IPC
// methods the CLI uses.
package mcp

import (
	"context"
	"fmt"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/martintrifunov/orkestar/internal/daemon"
	"github.com/martintrifunov/orkestar/internal/ipc"
	"github.com/martintrifunov/orkestar/internal/workflow"
)

// NewServer returns an MCP server whose tools are backed by the daemon
// reachable through client. The caller is responsible for running it over
// a transport (see sdk.Transport implementations, such as sdk.StdioTransport).
func NewServer(client *ipc.Client) *sdk.Server {
	server := sdk.NewServer(&sdk.Implementation{Name: "orkestar", Version: "v0.2.0"}, nil)

	sdk.AddTool(server, &sdk.Tool{
		Name:        "workspace_create",
		Description: "Create or reuse an Orkestar workspace rooted at a directory.",
	}, workspaceCreate(client))

	sdk.AddTool(server, &sdk.Tool{
		Name:        "task_create",
		Description: "Create a task in a workspace, optionally depending on other tasks. Requires a reviewer-agent verdict before it can be marked done unless auto_review is set to false.",
	}, taskCreate(client))

	sdk.AddTool(server, &sdk.Tool{
		Name:        "task_list",
		Description: "List tasks, optionally filtered to one workspace.",
	}, taskList(client))

	sdk.AddTool(server, &sdk.Tool{
		Name:        "task_set_status",
		Description: "Transition a task's status. Moving to in_progress requires its dependencies to be done; moving to done requires a reviewer-agent verdict unless the task opted out of auto review.",
	}, taskSetStatus(client))

	sdk.AddTool(server, &sdk.Tool{
		Name:        "task_assign",
		Description: "Assign a task to an agent by ID.",
	}, taskAssign(client))

	sdk.AddTool(server, &sdk.Tool{
		Name:        "task_create_worktree",
		Description: "Give a task its own git worktree and branch, sibling to its workspace directory.",
	}, taskCreateWorktree(client))

	sdk.AddTool(server, &sdk.Tool{
		Name:        "task_diff",
		Description: "Get the changed files and unified diff for a task's worktree.",
	}, taskDiff(client))

	sdk.AddTool(server, &sdk.Tool{
		Name:        "resource_acquire",
		Description: "Acquire a time-bounded shared or exclusive lease on a named resource.",
	}, resourceAcquire(client))

	sdk.AddTool(server, &sdk.Tool{
		Name:        "resource_release",
		Description: "Release a resource lease before it expires.",
	}, resourceRelease(client))

	sdk.AddTool(server, &sdk.Tool{
		Name:        "artifact_create",
		Description: "Record a durable artifact (diff, test result, log, screenshot, build, or review) against a task.",
	}, artifactCreate(client))

	return server
}

func callIPC[Out any](ctx context.Context, client *ipc.Client, method string, params any) (*sdk.CallToolResult, Out, error) {
	var out Out
	if err := client.Call(ctx, method, params, &out); err != nil {
		return nil, out, fmt.Errorf("%s: %w", method, err)
	}
	return nil, out, nil
}

type workspaceCreateInput struct {
	Directory string `json:"directory" jsonschema:"absolute or relative path to the workspace's working directory"`
}

func workspaceCreate(client *ipc.Client) sdk.ToolHandlerFor[workspaceCreateInput, daemon.Workspace] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in workspaceCreateInput) (*sdk.CallToolResult, daemon.Workspace, error) {
		return callIPC[daemon.Workspace](ctx, client, "workspace.create", map[string]string{"directory": in.Directory})
	}
}

type taskCreateInput struct {
	WorkspaceID string   `json:"workspace_id"`
	Title       string   `json:"title"`
	Description string   `json:"description,omitempty"`
	DependsOn   []string `json:"depends_on,omitempty" jsonschema:"IDs of tasks that must be done before this one can start"`
	AutoReview  *bool    `json:"auto_review,omitempty" jsonschema:"defaults to true; set false to allow marking the task done without a reviewer-agent verdict"`
}

func taskCreate(client *ipc.Client) sdk.ToolHandlerFor[taskCreateInput, workflow.Task] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in taskCreateInput) (*sdk.CallToolResult, workflow.Task, error) {
		autoReview := in.AutoReview == nil || *in.AutoReview
		return callIPC[workflow.Task](ctx, client, "task.create", map[string]any{
			"workspace_id": in.WorkspaceID,
			"title":        in.Title,
			"description":  in.Description,
			"depends_on":   in.DependsOn,
			"auto_review":  &autoReview,
		})
	}
}

type taskListInput struct {
	WorkspaceID string `json:"workspace_id,omitempty"`
}

type taskListOutput struct {
	Tasks []workflow.Task `json:"tasks"`
}

func taskList(client *ipc.Client) sdk.ToolHandlerFor[taskListInput, taskListOutput] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in taskListInput) (*sdk.CallToolResult, taskListOutput, error) {
		var snapshot daemon.Snapshot
		if err := client.Call(ctx, "system.snapshot", nil, &snapshot); err != nil {
			return nil, taskListOutput{}, fmt.Errorf("system.snapshot: %w", err)
		}
		if in.WorkspaceID == "" {
			return nil, taskListOutput{Tasks: snapshot.Tasks}, nil
		}
		filtered := make([]workflow.Task, 0, len(snapshot.Tasks))
		for _, task := range snapshot.Tasks {
			if task.WorkspaceID == in.WorkspaceID {
				filtered = append(filtered, task)
			}
		}
		return nil, taskListOutput{Tasks: filtered}, nil
	}
}

type taskSetStatusInput struct {
	TaskID string `json:"task_id"`
	Status string `json:"status" jsonschema:"one of pending, in_progress, done, cancelled"`
}

func taskSetStatus(client *ipc.Client) sdk.ToolHandlerFor[taskSetStatusInput, workflow.Task] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in taskSetStatusInput) (*sdk.CallToolResult, workflow.Task, error) {
		return callIPC[workflow.Task](ctx, client, "task.setStatus", map[string]string{
			"task_id": in.TaskID,
			"status":  in.Status,
		})
	}
}

type taskAssignInput struct {
	TaskID  string `json:"task_id"`
	AgentID string `json:"agent_id"`
}

func taskAssign(client *ipc.Client) sdk.ToolHandlerFor[taskAssignInput, workflow.Task] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in taskAssignInput) (*sdk.CallToolResult, workflow.Task, error) {
		return callIPC[workflow.Task](ctx, client, "task.assign", map[string]string{
			"task_id":  in.TaskID,
			"agent_id": in.AgentID,
		})
	}
}

type taskCreateWorktreeInput struct {
	TaskID string `json:"task_id"`
	Branch string `json:"branch,omitempty" jsonschema:"defaults to task/<task-id>"`
}

func taskCreateWorktree(client *ipc.Client) sdk.ToolHandlerFor[taskCreateWorktreeInput, workflow.Task] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in taskCreateWorktreeInput) (*sdk.CallToolResult, workflow.Task, error) {
		return callIPC[workflow.Task](ctx, client, "task.createWorktree", map[string]string{
			"task_id": in.TaskID,
			"branch":  in.Branch,
		})
	}
}

type taskDiffInput struct {
	TaskID string `json:"task_id"`
}

func taskDiff(client *ipc.Client) sdk.ToolHandlerFor[taskDiffInput, daemon.TaskDiff] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in taskDiffInput) (*sdk.CallToolResult, daemon.TaskDiff, error) {
		return callIPC[daemon.TaskDiff](ctx, client, "task.diff", map[string]string{"task_id": in.TaskID})
	}
}

type resourceAcquireInput struct {
	Resource   string `json:"resource"`
	HolderID   string `json:"holder_id"`
	Mode       string `json:"mode" jsonschema:"shared or exclusive"`
	DurationMS int64  `json:"duration_ms,omitempty" jsonschema:"lease lifetime in milliseconds; 0 means it never expires"`
}

func resourceAcquire(client *ipc.Client) sdk.ToolHandlerFor[resourceAcquireInput, workflow.Lease] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in resourceAcquireInput) (*sdk.CallToolResult, workflow.Lease, error) {
		return callIPC[workflow.Lease](ctx, client, "resource.acquire", map[string]any{
			"resource":    in.Resource,
			"holder_id":   in.HolderID,
			"mode":        in.Mode,
			"duration_ms": in.DurationMS,
		})
	}
}

type resourceReleaseInput struct {
	Resource string `json:"resource"`
	LeaseID  string `json:"lease_id"`
}

func resourceRelease(client *ipc.Client) sdk.ToolHandlerFor[resourceReleaseInput, map[string]string] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in resourceReleaseInput) (*sdk.CallToolResult, map[string]string, error) {
		return callIPC[map[string]string](ctx, client, "resource.release", map[string]string{
			"resource": in.Resource,
			"lease_id": in.LeaseID,
		})
	}
}

type artifactCreateInput struct {
	TaskID  string `json:"task_id"`
	Kind    string `json:"kind" jsonschema:"one of diff, test_result, log, screenshot, build, review"`
	Label   string `json:"label,omitempty"`
	Path    string `json:"path,omitempty" jsonschema:"path to the artifact on disk, for large content"`
	Content string `json:"content,omitempty" jsonschema:"small inline content, such as a short diff or log excerpt"`
}

func artifactCreate(client *ipc.Client) sdk.ToolHandlerFor[artifactCreateInput, workflow.Artifact] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in artifactCreateInput) (*sdk.CallToolResult, workflow.Artifact, error) {
		return callIPC[workflow.Artifact](ctx, client, "artifact.create", map[string]string{
			"task_id": in.TaskID,
			"kind":    in.Kind,
			"label":   in.Label,
			"path":    in.Path,
			"content": in.Content,
		})
	}
}
