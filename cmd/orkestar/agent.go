package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/martintrifunov/orkestar/internal/daemon"
	"github.com/martintrifunov/orkestar/internal/daemonclient"
	"github.com/martintrifunov/orkestar/internal/ipc"
	"github.com/martintrifunov/orkestar/internal/runtimepath"
)

// runHook is a private adapter bridge; configuration and credentials never pass
// through stdout. A missing daemon falls back to the agent's native prompt.
func runHook() error {
	var raw struct {
		SessionID    string `json:"session_id"`
		Event        string `json:"hook_event_name"`
		Tool         string `json:"tool_name"`
		Notification string `json:"notification_type"`
		SubagentID   string `json:"agent_id"`
		PermissionID string `json:"permission_id"`
	}
	if err := json.NewDecoder(io.LimitReader(os.Stdin, 1024*1024)).Decode(&raw); err != nil {
		return err
	}
	if raw.SubagentID != "" {
		_, _ = fmt.Fprintln(os.Stdout, "{}")
		return nil
	}
	input := daemon.HookInput{AgentID: os.Getenv("ORKESTAR_AGENT_ID"), Token: os.Getenv("ORKESTAR_HOOK_TOKEN"), Event: raw.Event, NativeSessionID: raw.SessionID, Tool: raw.Tool, Notification: raw.Notification, PermissionID: raw.PermissionID}
	ctx, cancel := context.WithTimeout(context.Background(), 9*time.Minute)
	defer cancel()
	var result map[string]string
	err := ipc.NewClient(os.Getenv("ORKESTAR_HOOK_SOCKET")).Call(ctx, "agent.hook", input, &result)
	var response any = map[string]any{}
	if err == nil && result["decision"] != "" {
		response = map[string]any{"hookSpecificOutput": map[string]any{"hookEventName": "PermissionRequest", "decision": map[string]string{"behavior": result["decision"]}}}
	}
	return json.NewEncoder(os.Stdout).Encode(response)
}
func runAgent(paths runtimepath.Paths, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: orkestar agent list | launch <workspace-id> <claude-code|opencode|codex> | resume <agent-id> | stop <agent-id> | remove <agent-id> | interrupt <agent-id>")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := daemonclient.Ensure(ctx, paths); err != nil {
		return err
	}
	client := ipc.NewClient(paths.Socket)
	var result any
	switch args[0] {
	case "list":
		var state daemon.Snapshot
		if err := client.Call(ctx, "system.snapshot", nil, &state); err != nil {
			return err
		}
		result = state.Agents
	case "launch":
		if len(args) < 3 || len(args) > 4 {
			return fmt.Errorf("usage: agent launch <workspace-id> <adapter> [--task=<task-id>]")
		}
		params := map[string]string{"workspace_id": args[1], "adapter": args[2], "mode": "interactive"}
		if len(args) == 4 {
			taskID, ok := strings.CutPrefix(args[3], "--task=")
			if !ok || taskID == "" {
				return fmt.Errorf("usage: agent launch <workspace-id> <adapter> [--task=<task-id>]")
			}
			// The daemon starts the session in the task's worktree and assigns
			// the task to it, so this is the whole hand-off.
			params["task_id"] = taskID
		}
		var a daemon.Agent
		if err := client.Call(ctx, "agent.launch", params, &a); err != nil {
			return err
		}
		result = a
	case "stop", "remove", "interrupt":
		if len(args) != 2 {
			return fmt.Errorf("usage: agent %s <agent-id>", args[0])
		}
		var outcome any
		if err := client.Call(ctx, "agent."+args[0], map[string]string{"agent_id": args[1]}, &outcome); err != nil {
			return err
		}
		result = outcome
	case "resume":
		if len(args) != 2 {
			return fmt.Errorf("usage: agent resume <agent-id>")
		}
		var a daemon.Agent
		if err := client.Call(ctx, "agent.resume", map[string]string{"agent_id": args[1]}, &a); err != nil {
			return err
		}
		result = a
	default:
		return fmt.Errorf("unknown agent command %q", args[0])
	}
	return json.NewEncoder(os.Stdout).Encode(result)
}
