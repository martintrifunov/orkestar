// Package mcp exposes Orkestar's daemon capabilities (workspaces, tasks,
// resource leases, artifacts) as MCP tools, so any MCP-capable agent can
// call them directly rather than only through the orkestar CLI or IPC. It
// is a thin translation layer: every tool forwards to the same daemon IPC
// methods the CLI uses.
package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/martintrifunov/orkestar/internal/agent"
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
		Name:        "task_update",
		Description: "Change a task's title, description or dependencies. Only the fields you send change; dependencies replace the existing list and are rejected if they would create a cycle.",
	}, taskUpdate(client))

	sdk.AddTool(server, &sdk.Tool{
		Name:        "task_start",
		Description: "Launch an agent to work a task, in the task's git worktree when it has one, assign the task to it, and tell it what to do. The prompt defaults to the task's own title and description, and is held until the agent's session reports it has started, so there is no need to wait before calling this. The task moves to in_progress once the agent acts on it. Use agent_list to see which adapters are available and what is already running.",
	}, taskStart(client))

	sdk.AddTool(server, &sdk.Tool{
		Name:        "agent_prompt",
		Description: "Send a prompt to a running agent, as a user typing into its session would. task_start already sends an opening prompt, so this is for following up.",
	}, agentPrompt(client))

	sdk.AddTool(server, &sdk.Tool{
		Name:        "agent_list",
		Description: "List running agent sessions with the task each is working, and the adapters available to launch.",
	}, agentList(client))

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

type taskUpdateInput struct {
	TaskID      string    `json:"task_id"`
	Title       *string   `json:"title,omitempty" jsonschema:"new title; omit to leave it unchanged"`
	Description *string   `json:"description,omitempty" jsonschema:"new description; omit to leave it unchanged, send an empty string to clear it"`
	DependsOn   *[]string `json:"depends_on,omitempty" jsonschema:"replaces the dependency list; omit to leave it unchanged, send an empty list to clear it"`
}

func taskUpdate(client *ipc.Client) sdk.ToolHandlerFor[taskUpdateInput, workflow.Task] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in taskUpdateInput) (*sdk.CallToolResult, workflow.Task, error) {
		// Only send the fields the caller set, so the daemon can tell an
		// absent field from one being cleared.
		params := map[string]any{"task_id": in.TaskID}
		if in.Title != nil {
			params["title"] = *in.Title
		}
		if in.Description != nil {
			params["description"] = *in.Description
		}
		if in.DependsOn != nil {
			params["depends_on"] = *in.DependsOn
		}
		return callIPC[workflow.Task](ctx, client, "task.update", params)
	}
}

type taskStartInput struct {
	TaskID  string  `json:"task_id"`
	Adapter string  `json:"adapter,omitempty" jsonschema:"which agent to launch; may be omitted when exactly one adapter is available"`
	Prompt  *string `json:"prompt,omitempty" jsonschema:"what to tell the agent; omit to send the task's own title and description, or pass an empty string to launch it idle"`
}

// taskStart launches an agent for a task. It resolves the workspace and the
// adapter from the snapshot rather than asking the caller for them: the task
// already knows its workspace, and an orchestrating agent should not have to
// guess an adapter name to find out which ones exist.
func taskStart(client *ipc.Client) sdk.ToolHandlerFor[taskStartInput, daemon.Agent] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in taskStartInput) (*sdk.CallToolResult, daemon.Agent, error) {
		var snapshot daemon.Snapshot
		if err := client.Call(ctx, "system.snapshot", nil, &snapshot); err != nil {
			return nil, daemon.Agent{}, fmt.Errorf("system.snapshot: %w", err)
		}

		var target workflow.Task
		for _, task := range snapshot.Tasks {
			if task.ID == in.TaskID {
				target = task
			}
		}
		if target.ID == "" {
			return nil, daemon.Agent{}, fmt.Errorf("task %q does not exist", in.TaskID)
		}

		adapter, mode, err := chooseAdapter(snapshot.Adapters, in.Adapter)
		if err != nil {
			return nil, daemon.Agent{}, err
		}
		prompt := briefing(target)
		if in.Prompt != nil {
			prompt = *in.Prompt
		}
		return callIPC[daemon.Agent](ctx, client, "agent.launch", map[string]any{
			"workspace_id": target.WorkspaceID,
			"adapter":      adapter,
			"mode":         mode,
			"task_id":      in.TaskID,
			"prompt":       prompt,
		})
	}
}

// briefing is what an agent is told when the caller does not say. A task's
// title and description are what it was written to convey, so repeating them
// is better than inventing instructions the orchestrator did not give.
func briefing(task workflow.Task) string {
	text := "You have been assigned this task: " + task.Title
	if task.Description != "" {
		text += "\n\n" + task.Description
	}
	return text
}

// chooseAdapter resolves a requested adapter name, or picks the only one when
// the caller named none. It returns the launch mode too, since an adapter that
// cannot run interactively has to be launched managed.
func chooseAdapter(available []agent.Capabilities, requested string) (string, string, error) {
	names := make([]string, 0, len(available))
	for _, capabilities := range available {
		names = append(names, capabilities.Name)
	}
	if len(available) == 0 {
		return "", "", errors.New("no agent adapters are registered")
	}
	if requested == "" {
		if len(available) != 1 {
			return "", "", fmt.Errorf("name an adapter: %s", strings.Join(names, ", "))
		}
		requested = available[0].Name
	}
	for _, capabilities := range available {
		if capabilities.Name != requested {
			continue
		}
		switch {
		case capabilities.SupportsInteractive:
			return requested, "interactive", nil
		case capabilities.SupportsManaged:
			return requested, "managed", nil
		}
		// Falling through to a mode the adapter rejects would surface as an
		// opaque launch failure instead of saying what is wrong.
		return "", "", fmt.Errorf("adapter %q supports neither interactive nor managed sessions", requested)
	}
	return "", "", fmt.Errorf("adapter %q is not registered; available: %s", requested, strings.Join(names, ", "))
}

type agentPromptInput struct {
	AgentID string `json:"agent_id"`
	Text    string `json:"text"`
}

// agentPrompt is what makes task_start a hand-off rather than just a launch:
// the agent is started for the task, and this is how it is told what to do.
func agentPrompt(client *ipc.Client) sdk.ToolHandlerFor[agentPromptInput, map[string]string] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, in agentPromptInput) (*sdk.CallToolResult, map[string]string, error) {
		return callIPC[map[string]string](ctx, client, "agent.prompt", map[string]string{
			"agent_id": in.AgentID,
			"text":     in.Text,
		})
	}
}

type agentListInput struct{}

type agentListOutput struct {
	Agents   []daemon.Agent       `json:"agents"`
	Adapters []agent.Capabilities `json:"adapters"`
}

func agentList(client *ipc.Client) sdk.ToolHandlerFor[agentListInput, agentListOutput] {
	return func(ctx context.Context, _ *sdk.CallToolRequest, _ agentListInput) (*sdk.CallToolResult, agentListOutput, error) {
		var snapshot daemon.Snapshot
		if err := client.Call(ctx, "system.snapshot", nil, &snapshot); err != nil {
			return nil, agentListOutput{}, fmt.Errorf("system.snapshot: %w", err)
		}
		return nil, agentListOutput{Agents: snapshot.Agents, Adapters: snapshot.Adapters}, nil
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
