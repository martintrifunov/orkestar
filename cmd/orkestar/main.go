package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/martintrifunov/orkestar/internal/agent/claude"
	"github.com/martintrifunov/orkestar/internal/agent/codex"
	"github.com/martintrifunov/orkestar/internal/agent/opencode"
	"github.com/martintrifunov/orkestar/internal/attach"
	"github.com/martintrifunov/orkestar/internal/daemon"
	"github.com/martintrifunov/orkestar/internal/daemonclient"
	"github.com/martintrifunov/orkestar/internal/ipc"
	orkestarmcp "github.com/martintrifunov/orkestar/internal/mcp"
	"github.com/martintrifunov/orkestar/internal/runtimepath"
	"github.com/martintrifunov/orkestar/internal/tui"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "orkestar: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 1 && (args[0] == "--version" || args[0] == "version" || args[0] == "-v") {
		fmt.Println("orkestar " + version)
		return nil
	}
	paths, err := runtimepath.Resolve()
	if err != nil {
		return err
	}

	if len(args) == 0 {
		return runTUI(paths)
	}

	switch args[0] {
	case "hook":
		return runHook()
	case "agent":
		return runAgent(paths, args[1:])
	case "daemon":
		if len(args) != 2 {
			return errors.New("usage: orkestar daemon serve|stop")
		}
		switch args[1] {
		case "serve":
			return serveDaemon(paths)
		case "stop":
			return stopDaemon(paths)
		default:
			return errors.New("usage: orkestar daemon serve|stop")
		}
	case "reset":
		return runReset(paths, args[1:])
	case "status":
		return printStatus(paths)
	case "workspace":
		return runWorkspace(paths, args[1:])
	case "terminal":
		return runTerminal(paths, args[1:])
	case "task":
		return runTask(paths, args[1:])
	case "mcp":
		return runMCP(paths, args[1:])
	case "help", "-h", "--help":
		printUsage()
		return nil
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runTUI(paths runtimepath.Paths) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := daemonclient.Ensure(ctx, paths); err != nil {
		return err
	}
	directory, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get current directory: %w", err)
	}
	return tui.Run(ipc.NewClient(paths.Socket), directory)
}

func runTerminal(paths runtimepath.Paths, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: orkestar terminal start <workspace-id> -- <command> [args...] | attach <terminal-id> | stop <terminal-id> | remove <terminal-id>")
	}

	switch args[0] {
	case "start":
		if len(args) < 3 {
			return errors.New("usage: orkestar terminal start <workspace-id> -- <command> [args...]")
		}
		commandIndex := 2
		if args[commandIndex] == "--" {
			commandIndex++
		}
		if commandIndex >= len(args) {
			return errors.New("terminal command is required")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var terminal daemon.Terminal
		if err := ipc.NewClient(paths.Socket).Call(ctx, "terminal.start", map[string]any{
			"workspace_id": args[1],
			"command":      args[commandIndex:],
			"columns":      80,
			"rows":         24,
		}, &terminal); err != nil {
			return err
		}
		fmt.Printf("%s\t%s\t%s\n", terminal.ID, terminal.State, terminal.Command[0])
		return nil
	case "stop", "remove":
		if len(args) != 2 {
			return fmt.Errorf("usage: orkestar terminal %s <terminal-id>", args[0])
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var result any
		if err := ipc.NewClient(paths.Socket).Call(ctx, "terminal."+args[0], map[string]any{"terminal_id": args[1]}, &result); err != nil {
			return err
		}
		past := map[string]string{"stop": "stopped", "remove": "removed"}[args[0]]
		fmt.Printf("%s %s\n", args[1], past)
		return nil
	case "attach":
		if len(args) != 2 {
			return errors.New("usage: orkestar terminal attach <terminal-id>")
		}
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		return attach.Terminal(ctx, ipc.NewClient(paths.Socket), args[1], os.Stdin, os.Stdout)
	default:
		return fmt.Errorf("unknown terminal command %q", args[0])
	}
}

func runMCP(paths runtimepath.Paths, args []string) error {
	if len(args) == 1 && args[0] == "instructions" {
		// The same text an MCP client is sent on connect, for an agent that
		// drives Orkestar through the CLI and never sees it.
		_, err := fmt.Println(orkestarmcp.Instructions())
		return err
	}
	if len(args) != 1 || args[0] != "serve" {
		return errors.New("usage: orkestar mcp serve|instructions")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := daemonclient.Ensure(ctx, paths); err != nil {
		return err
	}

	server := orkestarmcp.NewServer(ipc.NewClient(paths.Socket))
	runCtx, runCancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer runCancel()
	return server.Run(runCtx, &sdk.StdioTransport{})
}

func serveDaemon(paths runtimepath.Paths) error {
	if err := runtimepath.Ensure(paths); err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	server := daemon.NewServer(paths.Socket)
	server.SetVersion(version)
	server.RegisterAdapter(claude.New(""))
	server.RegisterAdapter(codex.New(""))
	server.RegisterAdapter(opencode.New("", "", nil))
	// OpenCode is the reviewer adapter because its managed mode returns a
	// structured reply; Claude Code's interactive PTY adapter has no
	// discrete response to parse a verdict from.
	server.SetReviewerAdapter("opencode")
	return server.Serve(ctx)
}

func stopDaemon(paths runtimepath.Paths) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var result map[string]string
	if err := ipc.NewClient(paths.Socket).Call(ctx, "system.shutdown", nil, &result); err != nil {
		return err
	}
	fmt.Println(result["status"])
	return nil
}

func printStatus(paths runtimepath.Paths) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	var result struct {
		Status  string `json:"status"`
		Version string `json:"version"`
	}
	if err := ipc.NewClient(paths.Socket).Call(ctx, "system.ping", nil, &result); err != nil {
		return fmt.Errorf("daemon is not available at %s: %w", paths.Socket, err)
	}
	if result.Version == "" {
		result.Version = "unversioned"
	}
	fmt.Printf("daemon %s, version %s; client %s (%s)\n", result.Status, result.Version, version, paths.Socket)
	return nil
}

func runWorkspace(paths runtimepath.Paths, args []string) error {
	if len(args) == 0 || args[0] != "create" {
		return errors.New("usage: orkestar workspace create [directory]")
	}
	if len(args) > 2 {
		return errors.New("usage: orkestar workspace create [directory]")
	}

	directory := "."
	if len(args) == 2 {
		directory = args[1]
	}
	directory, err := filepath.Abs(directory)
	if err != nil {
		return fmt.Errorf("resolve workspace directory: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var workspace daemon.Workspace
	if err := ipc.NewClient(paths.Socket).Call(ctx, "workspace.create", map[string]string{
		"directory": directory,
	}, &workspace); err != nil {
		return err
	}
	fmt.Printf("%s\t%s\t%s\n", workspace.ID, workspace.Name, workspace.Directory)
	return nil
}

func printUsage() {
	fmt.Print(`Orkestar coordinates persistent coding-agent sessions.

Usage:
  orkestar
  orkestar daemon serve
  orkestar daemon stop
  orkestar status
  orkestar reset [--yes]
  orkestar workspace create [directory]
  orkestar terminal start <workspace-id> -- <command> [args...]
  orkestar terminal attach <terminal-id>
  orkestar terminal stop <terminal-id>
  orkestar terminal remove <terminal-id>
  orkestar agent list
  orkestar agent launch <workspace-id> <claude-code|codex|opencode> [--task=<task-id>]
  orkestar agent resume <agent-id>
  orkestar agent stop <agent-id>
  orkestar agent remove <agent-id>
  orkestar agent interrupt <agent-id>
  orkestar task create <workspace-id> <title> [--depends-on id1,id2] [--no-review]
  orkestar task list [workspace-id]
  orkestar task edit <task-id> [--title=…] [--description=…] [--depends-on=id1,id2]
  orkestar task status <task-id> <pending|in_progress|done|cancelled>
  orkestar task assign <task-id> <agent-id>
  orkestar task worktree create <task-id> [branch]
  orkestar task worktree remove <task-id>
  orkestar task wait <task-id> [done|finished|startable] [--timeout=300]
  orkestar task diff <task-id>
  orkestar mcp serve
  orkestar mcp instructions
  orkestar help

Detach from an attached terminal with ctrl+b q.
`)
}

// runReset clears everything the daemon holds. It previews by default: a full
// reset is easy to ask for by accident and impossible to undo, so the state is
// only discarded once the caller has seen what will go and passed --yes.
func runReset(paths runtimepath.Paths, args []string) error {
	confirmed := false
	for _, argument := range args {
		switch argument {
		case "--yes", "-y":
			confirmed = true
		default:
			return fmt.Errorf("usage: orkestar reset [--yes]")
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := daemonclient.Ensure(ctx, paths); err != nil {
		return err
	}
	client := ipc.NewClient(paths.Socket)

	if !confirmed {
		var state daemon.Snapshot
		if err := client.Call(ctx, "system.snapshot", nil, &state); err != nil {
			return err
		}
		running := 0
		for _, terminal := range state.Terminals {
			if terminal.State == "running" {
				running++
			}
		}
		active := 0
		var worktrees []string
		for _, agent := range state.Agents {
			switch agent.State {
			case "stopped", "crashed", "interrupted":
			default:
				active++
			}
		}
		for _, task := range state.Tasks {
			if task.WorktreePath != "" {
				worktrees = append(worktrees, task.WorktreePath)
			}
		}
		if len(state.Terminals)+len(state.Agents)+len(state.Tasks)+len(state.Workspaces) == 0 {
			fmt.Println("Nothing to reset; the daemon is already empty.")
			return nil
		}
		fmt.Println("A reset stops and clears everything the daemon is holding:")
		fmt.Printf("  %-11s %d", "sessions", len(state.Terminals))
		if running > 0 {
			fmt.Printf("  (%d still running)", running)
		}
		fmt.Println()
		fmt.Printf("  %-11s %d", "agents", len(state.Agents))
		if active > 0 {
			fmt.Printf("  (%d still running)", active)
		}
		fmt.Println()
		for _, line := range []struct {
			label string
			count int
		}{
			{"tasks", len(state.Tasks)},
			{"artifacts", len(state.Artifacts)},
			{"workspaces", len(state.Workspaces)},
			{"leases", len(state.Leases)},
		} {
			fmt.Printf("  %-11s %d\n", line.label, line.count)
		}
		if len(worktrees) > 0 {
			fmt.Println("\nTask worktrees stay on disk and are not deleted:")
			for _, path := range worktrees {
				fmt.Println("  " + path)
			}
		}
		fmt.Println("\nNothing has changed. Run 'orkestar reset --yes' to go ahead.")
		return nil
	}

	// Always retire the running binary first. In particular, an older daemon
	// need not understand system.reset; only the fresh binary receives it.
	if err := daemonclient.Stop(ctx, paths.Socket); err != nil {
		return err
	}
	if err := daemonclient.Ensure(ctx, paths); err != nil {
		return err
	}
	var summary daemon.ResetSummary
	resetErr := client.Call(ctx, "system.reset", map[string]any{"confirm": true}, &summary)
	stopErr := daemonclient.Stop(ctx, paths.Socket)
	if err := errors.Join(resetErr, stopErr); err != nil {
		return err
	}
	fmt.Println("Reset complete. Daemon stopped. Cleared:")
	for _, line := range []struct {
		label string
		count int
	}{
		{"sessions", summary.Terminals},
		{"agents", summary.Agents},
		{"tasks", summary.Tasks},
		{"artifacts", summary.Artifacts},
		{"workspaces", summary.Workspaces},
		{"leases", summary.Leases},
	} {
		fmt.Printf("  %-11s %d\n", line.label, line.count)
	}
	if len(summary.Worktrees) > 0 {
		fmt.Println("\nThese task worktrees were left on disk:")
		for _, path := range summary.Worktrees {
			fmt.Println("  " + path)
		}
		fmt.Println("Remove one with 'git worktree remove <path>' if you no longer need it.")
	}
	return nil
}
