package federation

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/martintrifunov/orkestar/internal/daemon"
	"github.com/martintrifunov/orkestar/internal/ipc"
	"github.com/martintrifunov/orkestar/internal/machine"
)

func serveFederationDaemon(t *testing.T) (*ipc.Client, func()) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "orkestar-fed-")
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(dir, "orkestar.sock")
	server := daemon.NewServer(socket)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx) }()
	client := ipc.NewClient(socket)

	deadline := time.Now().Add(3 * time.Second)
	for {
		pingContext, pingCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		err := client.Call(pingContext, "system.ping", nil, new(map[string]string))
		pingCancel()
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("daemon did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}

	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			select {
			case err := <-done:
				if err != nil {
					t.Errorf("server shutdown: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Error("server did not stop")
			}
			_ = os.RemoveAll(dir)
		})
	}
	t.Cleanup(stop)
	return client, stop
}

func createWorkspace(t *testing.T, client *ipc.Client, directory string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := client.Call(ctx, "workspace.create", map[string]string{"directory": directory}, new(daemon.Workspace)); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
}

func multiDialer(clients map[string]*ipc.Client) Dialer {
	return func(_ context.Context, saved machine.Machine) (*ipc.Client, error) {
		client, ok := clients[saved.Host]
		if !ok {
			return nil, fmt.Errorf("unknown host %q", saved.Host)
		}
		return client, nil
	}
}

func TestBoardMergesMachinesAndRoutes(t *testing.T) {
	local, _ := serveFederationDaemon(t)
	one, _ := serveFederationDaemon(t)
	two, _ := serveFederationDaemon(t)
	createWorkspace(t, local, t.TempDir())
	createWorkspace(t, one, t.TempDir())
	createWorkspace(t, two, t.TempDir())

	manager := New("Local", local, multiDialer(map[string]*ipc.Client{"one": one, "two": two}))
	manager.SetMachines([]machine.Machine{
		{ID: "m1", Label: "One", Host: "one", Enabled: true},
		{ID: "m2", Label: "Two", Host: "two", Enabled: true},
	})
	manager.Refresh(context.Background())

	board := manager.Board()
	if len(board.Machines) != 3 {
		t.Fatalf("expected three machines, got %#v", board.Machines)
	}
	for _, status := range board.Machines {
		if status.State != Online {
			t.Fatalf("expected every machine online: %#v", board.Machines)
		}
	}
	if len(board.Workspaces) != 3 {
		t.Fatalf("expected three workspaces merged, got %#v", board.Workspaces)
	}
	labels := map[string]bool{}
	for _, workspace := range board.Workspaces {
		labels[workspace.MachineLabel] = true
	}
	if !labels["Local"] || !labels["One"] || !labels["Two"] {
		t.Fatalf("a machine's workspace is missing: %#v", board.Workspaces)
	}

	if client, ok := manager.ClientFor(LocalID); !ok || client != local {
		t.Fatal("local routing returned the wrong client")
	}
	if client, ok := manager.ClientFor("m1"); !ok || client != one {
		t.Fatal("remote routing returned the wrong client")
	}
	if _, ok := manager.ClientFor("missing"); ok {
		t.Fatal("an unknown machine should not route")
	}
}

func TestRemoteReconnectsAfterBackoff(t *testing.T) {
	local, _ := serveFederationDaemon(t)
	one, _ := serveFederationDaemon(t)
	dials := 0
	dial := func(_ context.Context, _ machine.Machine) (*ipc.Client, error) {
		dials++
		if dials == 1 {
			return nil, errors.New("host unreachable")
		}
		return one, nil
	}

	manager := New("Local", local, dial)
	manager.SetBackoff(10 * time.Millisecond)
	manager.SetMachines([]machine.Machine{{ID: "m1", Label: "One", Host: "one", Enabled: true}})

	manager.Refresh(context.Background())
	if state := manager.Status()[1].State; state != Offline {
		t.Fatalf("expected the unreachable machine to be offline, got %q", state)
	}

	// An immediate refresh must not redial: the backoff has not elapsed.
	manager.Refresh(context.Background())
	if dials != 1 {
		t.Fatalf("redialed before the backoff elapsed: %d", dials)
	}

	time.Sleep(30 * time.Millisecond)
	manager.Refresh(context.Background())
	if dials != 2 {
		t.Fatalf("expected a redial after the backoff, got %d", dials)
	}
	if state := manager.Status()[1].State; state != Online {
		t.Fatalf("expected the machine back online, got %q", state)
	}
}

func TestBoardDropsAnUnreachableMachineWithoutMovingOthers(t *testing.T) {
	local, _ := serveFederationDaemon(t)
	one, _ := serveFederationDaemon(t)
	two, stopTwo := serveFederationDaemon(t)
	createWorkspace(t, local, t.TempDir())
	createWorkspace(t, one, t.TempDir())
	createWorkspace(t, two, t.TempDir())

	manager := New("Local", local, multiDialer(map[string]*ipc.Client{"one": one, "two": two}))
	manager.SetMachines([]machine.Machine{
		{ID: "m1", Label: "One", Host: "one", Enabled: true},
		{ID: "m2", Label: "Two", Host: "two", Enabled: true},
	})
	manager.Refresh(context.Background())
	if len(manager.Board().Workspaces) != 3 {
		t.Fatalf("expected all three workspaces before the loss")
	}

	stopTwo()
	manager.Refresh(context.Background())

	board := manager.Board()
	if len(board.Workspaces) != 2 {
		t.Fatalf("expected the lost machine's workspace to drop, got %#v", board.Workspaces)
	}
	for _, workspace := range board.Workspaces {
		if workspace.MachineLabel == "Two" {
			t.Fatalf("the offline machine still contributes: %#v", board.Workspaces)
		}
	}
	online := map[string]bool{}
	for _, status := range board.Machines {
		if status.State == Online {
			online[status.Machine.Label] = true
		}
	}
	if !online["Local"] || !online["One"] {
		t.Fatalf("losing one machine moved the others: %#v", board.Machines)
	}
}

func TestSetMachinesForgetsDisabled(t *testing.T) {
	local, _ := serveFederationDaemon(t)
	one, _ := serveFederationDaemon(t)
	manager := New("Local", local, multiDialer(map[string]*ipc.Client{"one": one}))
	manager.SetMachines([]machine.Machine{{ID: "m1", Label: "One", Host: "one", Enabled: true}})
	manager.Refresh(context.Background())
	if len(manager.Status()) != 2 {
		t.Fatalf("expected local and one, got %#v", manager.Status())
	}

	manager.SetMachines([]machine.Machine{{ID: "m1", Label: "One", Host: "one", Enabled: false}})
	manager.Refresh(context.Background())
	if len(manager.Status()) != 1 {
		t.Fatalf("expected the disabled machine to be forgotten, got %#v", manager.Status())
	}
}

// Two machines must be dialed concurrently: a serial refresh pays every
// dead host's timeouts before reaching the next machine.
func TestRefreshDialsMachinesConcurrently(t *testing.T) {
	local, _ := serveFederationDaemon(t)
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	dial := func(_ context.Context, _ machine.Machine) (*ipc.Client, error) {
		entered <- struct{}{}
		<-release
		return nil, errors.New("host unreachable")
	}

	manager := New("Local", local, dial)
	manager.SetMachines([]machine.Machine{
		{ID: "m1", Label: "One", Host: "one", Enabled: true},
		{ID: "m2", Label: "Two", Host: "two", Enabled: true},
	})
	done := make(chan struct{})
	go func() { manager.Refresh(context.Background()); close(done) }()

	deadline := time.Now().Add(5 * time.Second)
	for len(entered) < 2 {
		if time.Now().After(deadline) {
			close(release)
			t.Fatal("the second machine was not dialed until the first finished")
		}
		time.Sleep(10 * time.Millisecond)
	}
	close(release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("refresh did not finish")
	}
}

// The backoff loop can double once past its cap; retryAt must still promise
// no more than a minute out.
func TestBackoffNeverExceedsOneMinute(t *testing.T) {
	manager := New("Local", nil, nil)
	connection := &Connection{failures: 10}
	manager.recordFailureLocked(connection, errors.New("boom"))
	if wait := time.Until(connection.retryAt); wait > time.Minute+5*time.Second || wait < 55*time.Second {
		t.Fatalf("expected about a minute of backoff, got %s", wait)
	}
}

// A catalog edit that moves a host under a stable ID must drop the old
// client: otherwise calls keep going to the old machine.
func TestSetMachinesRedialsWhenHostChanges(t *testing.T) {
	local, _ := serveFederationDaemon(t)
	one, _ := serveFederationDaemon(t)
	two, _ := serveFederationDaemon(t)
	var dialed []string
	dial := func(_ context.Context, saved machine.Machine) (*ipc.Client, error) {
		dialed = append(dialed, saved.Host)
		return map[string]*ipc.Client{"one": one, "two": two}[saved.Host], nil
	}

	manager := New("Local", local, dial)
	manager.SetMachines([]machine.Machine{{ID: "m1", Label: "One", Host: "one", Enabled: true}})
	manager.Refresh(context.Background())
	if client, ok := manager.ClientFor("m1"); !ok || client != one {
		t.Fatal("expected routing to the first host")
	}

	manager.SetMachines([]machine.Machine{{ID: "m1", Label: "One", Host: "two", Enabled: true}})
	if connection := manager.remotes["m1"]; connection.client != nil {
		t.Fatal("the old host's client survived the move")
	}
	manager.Refresh(context.Background())
	if client, ok := manager.ClientFor("m1"); !ok || client != two {
		t.Fatal("expected routing to the moved host")
	}
}
func TestIsAttention(t *testing.T) {
	for state, want := range map[string]bool{
		"waiting_input": true, "waiting_permission": true, "waiting_resource": true,
		"working": false, "ready": false, "stopped": false,
	} {
		if got := isAttention(state); got != want {
			t.Errorf("isAttention(%q) = %v, want %v", state, got, want)
		}
	}
}
