package daemonclient

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/martintrifunov/orkestar/internal/ipc"
	"github.com/martintrifunov/orkestar/internal/runtimepath"
)

func Ensure(ctx context.Context, paths runtimepath.Paths) error {
	if daemonAvailable(ctx, paths.Socket) {
		return nil
	}
	if err := runtimepath.Ensure(paths); err != nil {
		return err
	}

	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("find Orkestar executable: %w", err)
	}
	logFile, err := os.OpenFile(paths.Log, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open daemon log: %w", err)
	}

	command := exec.Command(executable, "daemon", "serve")
	command.Stdin = nil
	command.Stdout = logFile
	command.Stderr = logFile
	configureDaemon(command)
	if err := command.Start(); err != nil {
		logFile.Close()
		return fmt.Errorf("start daemon: %w", err)
	}
	// Reap a daemon we started if this client remains alive through shutdown.
	// Exiting the client still leaves the detached daemon running.
	go func() { _ = command.Wait() }()
	_ = logFile.Close()

	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if daemonAvailable(ctx, paths.Socket) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
	return fmt.Errorf("daemon did not become ready; inspect %s", paths.Log)
}

func daemonAvailable(parent context.Context, socketPath string) bool {
	ctx, cancel := context.WithTimeout(parent, 150*time.Millisecond)
	defer cancel()
	var result map[string]string
	return ipc.NewClient(socketPath).Call(ctx, "system.ping", nil, &result) == nil
}
