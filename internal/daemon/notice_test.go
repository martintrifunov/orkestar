package daemon

import (
	"context"
	"encoding/json"
	"net"
	"path/filepath"
	"testing"
)

// The daemon posts a notification only when no client is attached: an
// attached interface knows whether the terminal is focused and owns delivery.
func TestDetachedNotificationsFireOnlyWithoutClients(t *testing.T) {
	s := NewServer(filepath.Join(t.TempDir(), "socket"))
	var posted []string
	s.SetNotifier(func(_, body string) { posted = append(posted, body) })
	s.workspaces["ws"] = Workspace{ID: "ws", Directory: t.TempDir()}

	done, err := s.tasks.Create("ws", "Ship it", "", nil, false)
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	params, _ := json.Marshal(map[string]string{"task_id": done.ID, "status": "done"})
	if _, err := s.setTaskStatus(context.Background(), params); err != nil {
		t.Fatalf("set status: %v", err)
	}
	if len(posted) != 1 {
		t.Fatalf("expected one notification for the finished task, got %v", posted)
	}

	// With a client attached the interface takes over.
	serverConnection, clientConnection := net.Pipe()
	defer serverConnection.Close()
	defer clientConnection.Close()
	s.connections[serverConnection] = struct{}{}

	second, err := s.tasks.Create("ws", "Later", "", nil, false)
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	params, _ = json.Marshal(map[string]string{"task_id": second.ID, "status": "done"})
	if _, err := s.setTaskStatus(context.Background(), params); err != nil {
		t.Fatalf("set status: %v", err)
	}
	if len(posted) != 1 {
		t.Fatalf("a notification fired while a client was attached: %v", posted)
	}

	// An agent stopping with its task open is the other moment worth posting.
	open, err := s.tasks.Create("ws", "Unfinished", "", nil, false)
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	stopped := Agent{ID: "a", Adapter: "claude-code", TaskID: open.ID, State: "stopped"}
	s.announceAgentStopped(stopped)
	if len(posted) != 1 {
		t.Fatalf("a notification fired while a client was attached: %v", posted)
	}

	delete(s.connections, serverConnection)
	s.announceAgentStopped(stopped)
	if len(posted) != 2 {
		t.Fatalf("expected the detached stop to post, got %v", posted)
	}

	// An agent that stopped after its task was finished is ordinary.
	finished := Agent{ID: "b", Adapter: "claude-code", TaskID: done.ID, State: "stopped"}
	s.announceAgentStopped(finished)
	if len(posted) != 2 {
		t.Fatalf("a finished task's agent posted a notification: %v", posted)
	}
}
