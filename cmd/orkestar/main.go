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

	"github.com/martintrifunov/orkestar/internal/daemon"
	"github.com/martintrifunov/orkestar/internal/ipc"
	"github.com/martintrifunov/orkestar/internal/runtimepath"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "orkestar: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		printUsage()
		return nil
	}

	paths, err := runtimepath.Resolve()
	if err != nil {
		return err
	}

	switch args[0] {
	case "daemon":
		if len(args) != 2 || args[1] != "serve" {
			return errors.New("usage: orkestar daemon serve")
		}
		return serveDaemon(paths)
	case "status":
		return printStatus(paths)
	case "workspace":
		return runWorkspace(paths, args[1:])
	case "help", "-h", "--help":
		printUsage()
		return nil
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func serveDaemon(paths runtimepath.Paths) error {
	if err := runtimepath.Ensure(paths); err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	return daemon.NewServer(paths.Socket).Serve(ctx)
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
  orkestar daemon serve
  orkestar status
  orkestar workspace create [directory]
  orkestar help
`)
}
