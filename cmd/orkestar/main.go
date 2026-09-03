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

	"github.com/martintrifunov/orkestar/internal/agent/claude"
	"github.com/martintrifunov/orkestar/internal/agent/opencode"
	"github.com/martintrifunov/orkestar/internal/attach"
	"github.com/martintrifunov/orkestar/internal/daemon"
	"github.com/martintrifunov/orkestar/internal/daemonclient"
	"github.com/martintrifunov/orkestar/internal/ipc"
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
	paths, err := runtimepath.Resolve()
	if err != nil {
		return err
	}

	if len(args) == 0 {
		return runTUI(paths)
	}

	switch args[0] {
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
	case "status":
		return printStatus(paths)
	case "workspace":
		return runWorkspace(paths, args[1:])
	case "terminal":
		return runTerminal(paths, args[1:])
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
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find Orkestar executable: %w", err)
	}
	return tui.Run(ipc.NewClient(paths.Socket), directory, executable)
}

func runTerminal(paths runtimepath.Paths, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: orkestar terminal start <workspace-id> -- <command> [args...] | orkestar terminal attach <terminal-id>")
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

func serveDaemon(paths runtimepath.Paths) error {
	if err := runtimepath.Ensure(paths); err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	server := daemon.NewServer(paths.Socket)
	server.RegisterAdapter(claude.New(""))
	server.RegisterAdapter(opencode.New("", nil))
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
		Status string `json:"status"`
	}
	if err := ipc.NewClient(paths.Socket).Call(ctx, "system.ping", nil, &result); err != nil {
		return fmt.Errorf("daemon is not available at %s: %w", paths.Socket, err)
	}
	fmt.Printf("daemon %s (%s)\n", result.Status, paths.Socket)
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
  orkestar workspace create [directory]
  orkestar terminal start <workspace-id> -- <command> [args...]
  orkestar terminal attach <terminal-id>
  orkestar help

Detach from an attached terminal with ctrl+b q.
`)
}
