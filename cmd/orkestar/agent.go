package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/martintrifunov/orkestar/internal/agent"
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
		// Providers that write a transcript name it here; the daemon reads
		// token usage from it. Bridges that compute their own total send
		// tokens instead.
		TranscriptPath string          `json:"transcript_path"`
		Tokens         int64           `json:"tokens"`
		ToolInput      json.RawMessage `json:"tool_input"`
	}
	if err := json.NewDecoder(io.LimitReader(os.Stdin, 1024*1024)).Decode(&raw); err != nil {
		return err
	}
	if raw.SubagentID != "" {
		_, _ = fmt.Fprintln(os.Stdout, "{}")
		return nil
	}
	input := daemon.HookInput{AgentID: os.Getenv("ORKESTAR_AGENT_ID"), Token: os.Getenv("ORKESTAR_HOOK_TOKEN"), Event: raw.Event, NativeSessionID: raw.SessionID, Tool: raw.Tool, Notification: raw.Notification, PermissionID: raw.PermissionID, TranscriptPath: raw.TranscriptPath, Tokens: raw.Tokens, Target: hookTarget(raw.ToolInput)}
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

// hookTarget extracts the command or path a permission is about, so policy
// rules have something stable to match. Providers name it in different fields
// under tool_input; anything unrecognized falls back to the compact JSON,
// which a prefix glob can still match.
func hookTarget(toolInput json.RawMessage) string {
	if len(toolInput) == 0 {
		return ""
	}
	var fields struct {
		Command  string `json:"command"`
		FilePath string `json:"file_path"`
		Path     string `json:"path"`
	}
	if err := json.Unmarshal(toolInput, &fields); err == nil {
		switch {
		case fields.Command != "":
			return fields.Command
		case fields.FilePath != "":
			return fields.FilePath
		case fields.Path != "":
			return fields.Path
		}
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, toolInput); err != nil {
		return ""
	}
	target := compact.String()
	if len(target) > 512 {
		target = target[:512]
	}
	return target
}

func runAgent(paths runtimepath.Paths, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: orkestar agent list | launch <workspace-id> <adapter> [--task=<task-id>] | resume <agent-id> | explain <agent-id> | reload | stop <agent-id> | remove <agent-id> | interrupt <agent-id>")
	}
	// Catch junk args before starting a daemon for them.
	if (args[0] == "list" || args[0] == "reload") && len(args) != 1 {
		return fmt.Errorf("usage: agent %s", args[0])
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
		if len(args) != 1 {
			return fmt.Errorf("usage: agent list")
		}
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
	case "explain":
		if len(args) != 2 {
			return fmt.Errorf("usage: agent explain <agent-id>")
		}
		var explanation daemon.AgentExplanation
		if err := client.Call(ctx, "agent.explain", map[string]string{"agent_id": args[1]}, &explanation); err != nil {
			return err
		}
		result = explanation
	case "reload":
		if len(args) != 1 {
			return fmt.Errorf("usage: agent reload")
		}
		var capabilities []agent.Capabilities
		if err := client.Call(ctx, "agent.reloadAdapters", nil, &capabilities); err != nil {
			return err
		}
		result = capabilities
	default:
		return fmt.Errorf("unknown agent command %q", args[0])
	}
	return json.NewEncoder(os.Stdout).Encode(result)
}
