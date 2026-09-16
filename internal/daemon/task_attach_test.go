package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/martintrifunov/orkestar/internal/ipc"
	"github.com/martintrifunov/orkestar/internal/workflow"
)

// task.attach is the subscription counterpart to task.wait: a waiter follows
// one task, and this follows the whole board, announcing every change.
func TestTaskAttachStreamsBoardChanges(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "orkestar-task-attach-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	_, client, _ := serveRecoveryTest(t, filepath.Join(dir, "socket"))
	var w Workspace
	callRecovery(t, client, "workspace.create", map[string]string{"directory": dir}, &w)

	var initial struct {
		Tasks []workflow.Task `json:"tasks"`
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	stream, err := client.OpenStream(ctx, "task.attach", map[string]any{}, &initial)
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	defer stream.Close()
	if len(initial.Tasks) != 0 {
		t.Fatalf("expected an empty board, got %#v", initial.Tasks)
	}

	updated := make(chan []workflow.Task, 8)
	go func() {
		for {
			var event ipc.Event
			if stream.Receive(&event) != nil {
				return
			}
			if event.Event != "task.updated" {
				continue
			}
			var payload struct {
				Tasks []workflow.Task `json:"tasks"`
			}
			if json.Unmarshal(event.Data, &payload) != nil {
				return
			}
			updated <- payload.Tasks
		}
	}()

	var created workflow.Task
	callRecovery(t, client, "task.create", map[string]any{
		"workspace_id": w.ID, "title": "streamed", "auto_review": false,
	}, &created)

	select {
	case tasks := <-updated:
		found := false
		for _, task := range tasks {
			if task.ID == created.ID {
				found = true
			}
		}
		if !found {
			t.Fatalf("task.updated did not include the new task: %#v", tasks)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no task.updated event after a board change")
	}
}
