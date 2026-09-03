package daemon_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/martintrifunov/orkestar/internal/daemon"
	"github.com/martintrifunov/orkestar/internal/ipc"
)

func TestServerPingAndWorkspaceLifecycle(t *testing.T) {
	t.Parallel()

	temporaryDirectory := t.TempDir()
	socketDirectory, err := os.MkdirTemp("/tmp", "orkestar-test-")
	if err != nil {
		t.Fatalf("create socket directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDirectory) })
	socketPath := filepath.Join(socketDirectory, "orkestar.sock")
	server := daemon.NewServer(socketPath)
	ctx, cancel := context.WithCancel(context.Background())
	serverError := make(chan error, 1)
	go func() {
		serverError <- server.Serve(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		if err := <-serverError; err != nil {
			t.Errorf("server shutdown: %v", err)
		}
	})

	client := ipc.NewClient(socketPath)
	waitForServer(t, client)

	var ping struct {
		Status string `json:"status"`
	}
	callContext, callCancel := context.WithTimeout(context.Background(), time.Second)
	defer callCancel()
	if err := client.Call(callContext, "system.ping", nil, &ping); err != nil {
		t.Fatalf("ping daemon: %v", err)
	}
	if ping.Status != "ok" {
		t.Fatalf("unexpected ping status %q", ping.Status)
	}

	var workspace daemon.Workspace
	if err := client.Call(callContext, "workspace.create", map[string]string{
		"directory": temporaryDirectory,
		"name":      "test",
	}, &workspace); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if workspace.Name != "test" || workspace.Directory != temporaryDirectory {
		t.Fatalf("unexpected workspace: %#v", workspace)
	}

	var snapshot daemon.Snapshot
	if err := client.Call(callContext, "system.snapshot", nil, &snapshot); err != nil {
		t.Fatalf("get snapshot: %v", err)
	}
	if len(snapshot.Workspaces) != 1 || snapshot.Workspaces[0].ID != workspace.ID {
		t.Fatalf("unexpected snapshot: %#v", snapshot)
	}
}

func waitForServer(t *testing.T, client *ipc.Client) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		var result map[string]string
		err := client.Call(ctx, "system.ping", nil, &result)
		cancel()
		if err == nil {
			return
		}
		if !ipc.IsUnavailable(err) && !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("wait for server: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("server did not become ready")
}
