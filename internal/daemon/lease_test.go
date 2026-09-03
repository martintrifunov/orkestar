package daemon_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/martintrifunov/orkestar/internal/daemon"
	"github.com/martintrifunov/orkestar/internal/ipc"
	"github.com/martintrifunov/orkestar/internal/workflow"
)

func TestResourceLeaseAcquireAndRelease(t *testing.T) {
	t.Parallel()

	socketDirectory, err := os.MkdirTemp("/tmp", "orkestar-lease-test-")
	if err != nil {
		t.Fatalf("create socket directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDirectory) })
	socketPath := filepath.Join(socketDirectory, "orkestar.sock")

	server := daemon.NewServer(socketPath)
	ctx, cancel := context.WithCancel(context.Background())
	serverError := make(chan error, 1)
	go func() { serverError <- server.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-serverError; err != nil {
			t.Errorf("server shutdown: %v", err)
		}
	})

	client := ipc.NewClient(socketPath)
	waitForServer(t, client)
	callContext, callCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer callCancel()

	var lease workflow.Lease
	if err := client.Call(callContext, "resource.acquire", map[string]any{
		"resource":    "unreal-editor",
		"holder_id":   "agent_1",
		"mode":        string(workflow.LeaseExclusive),
		"duration_ms": 60000,
	}, &lease); err != nil {
		t.Fatalf("acquire lease: %v", err)
	}
	if lease.Resource != "unreal-editor" || lease.HolderID != "agent_1" {
		t.Fatalf("unexpected lease: %#v", lease)
	}

	var conflict workflow.Lease
	if err := client.Call(callContext, "resource.acquire", map[string]any{
		"resource":    "unreal-editor",
		"holder_id":   "agent_2",
		"mode":        string(workflow.LeaseExclusive),
		"duration_ms": 60000,
	}, &conflict); err == nil {
		t.Fatal("expected conflicting exclusive lease to be rejected")
	}

	var snapshot daemon.Snapshot
	if err := client.Call(callContext, "system.snapshot", nil, &snapshot); err != nil {
		t.Fatalf("get snapshot: %v", err)
	}
	if len(snapshot.Leases) != 1 || snapshot.Leases[0].ID != lease.ID {
		t.Fatalf("unexpected leases in snapshot: %#v", snapshot.Leases)
	}

	var released map[string]string
	if err := client.Call(callContext, "resource.release", map[string]string{
		"resource": "unreal-editor",
		"lease_id": lease.ID,
	}, &released); err != nil {
		t.Fatalf("release lease: %v", err)
	}
	if released["status"] != "released" {
		t.Fatalf("unexpected release status: %q", released["status"])
	}

	if err := client.Call(callContext, "resource.acquire", map[string]any{
		"resource":    "unreal-editor",
		"holder_id":   "agent_2",
		"mode":        string(workflow.LeaseExclusive),
		"duration_ms": 60000,
	}, &conflict); err != nil {
		t.Fatalf("acquire lease after release: %v", err)
	}
}
