package daemon

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/martintrifunov/orkestar/internal/ipc"
	"github.com/martintrifunov/orkestar/internal/workflow"
)

// sendRawWait writes one wait request on its own connection and returns the
// connection without reading the reply, so the test can go away mid-wait the
// way a Ctrl-C'd CLI does.
func sendRawWait(t *testing.T, socket, method string, params map[string]any) net.Conn {
	t.Helper()
	connection, err := net.Dial("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	request := ipc.Request{ID: "req_wait", Version: ipc.Version, Method: method, Params: encoded}
	if err := json.NewEncoder(connection).Encode(request); err != nil {
		_ = connection.Close()
		t.Fatal(err)
	}
	return connection
}

func terminalSubscribers(server *Server, id string) int {
	server.mu.RLock()
	session := server.terminals[id]
	server.mu.RUnlock()
	if session == nil {
		return -1
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	return len(session.subscribers)
}

func waitForCondition(t *testing.T, what string, timeout time.Duration, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if check() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s did not settle in time", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A client that goes away mid-wait must release the wait: the terminal
// subscriber and the task watcher are daemon resources, and holding them
// until the timeout leaks one per interrupted orchestration call.
func TestWaitsReleasedOnClientDisconnect(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "orkestar-wait-disconnect-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	socket := filepath.Join(dir, "socket")
	server, client, _ := serveRecoveryTest(t, socket)

	var w Workspace
	callRecovery(t, client, "workspace.create", map[string]string{"directory": dir}, &w)
	var started Terminal
	callRecovery(t, client, "terminal.start", map[string]any{
		"workspace_id": w.ID,
		"command":      []string{"/bin/sh", "-c", "sleep 60"},
	}, &started)

	waiter := sendRawWait(t, socket, "terminal.wait", map[string]any{
		"terminal_id": started.ID, "contains": "never-printed", "timeout_seconds": 60,
	})
	waitForCondition(t, "terminal wait to subscribe", 3*time.Second, func() bool {
		return terminalSubscribers(server, started.ID) == 1
	})
	_ = waiter.Close()
	waitForCondition(t, "terminal wait to release on disconnect", 5*time.Second, func() bool {
		return terminalSubscribers(server, started.ID) == 0
	})

	var task workflow.Task
	callRecovery(t, client, "task.create", map[string]any{
		"workspace_id": w.ID, "title": "waited on", "auto_review": false,
	}, &task)
	// The daemon keeps a watcher of its own for auto-start tasks, so the
	// count to compare against is the baseline it holds, not zero.
	waitForCondition(t, "the daemon's own watcher to register", 3*time.Second, func() bool {
		return server.tasks.Watchers() >= 1
	})
	base := server.tasks.Watchers()
	taskWaiter := sendRawWait(t, socket, "task.wait", map[string]any{
		"task_id": task.ID, "until": "done", "timeout_seconds": 60,
	})
	waitForCondition(t, "task wait to register", 3*time.Second, func() bool {
		return server.tasks.Watchers() == base+1
	})
	_ = taskWaiter.Close()
	waitForCondition(t, "task wait to release on disconnect", 5*time.Second, func() bool {
		return server.tasks.Watchers() == base
	})
}
