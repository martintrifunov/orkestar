package daemon_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/martintrifunov/orkestar/internal/daemon"
)

func TestTerminalRecordingWritesAnAsciicast(t *testing.T) {
	t.Parallel()

	fixture := newTaskAgentFixture(t)
	task := fixture.createTask(t, "recorded work")

	// An interactive shell with no output of its own: recording starts first,
	// then the command is typed in, so the capture cannot race the output it
	// is supposed to contain.
	var terminal daemon.Terminal
	fixture.mustCall(t, "terminal.start", map[string]any{
		"workspace_id": fixture.workspace.ID,
		"command":      []string{"/bin/sh"},
	}, &terminal)

	var started daemon.RecordingStatus
	fixture.mustCall(t, "terminal.record", map[string]any{
		"terminal_id": terminal.ID, "action": "start", "task_id": task.ID,
	}, &started)
	if !started.Recording || started.Path == "" {
		t.Fatalf("recording did not start: %+v", started)
	}
	var sent map[string]string
	fixture.mustCall(t, "terminal.send", map[string]any{
		"terminal_id": terminal.ID, "text": "printf 'recording-needle\\n'", "enter": true,
	}, &sent)

	deadline := time.Now().Add(5 * time.Second)
	for {
		content, err := os.ReadFile(started.Path)
		if err == nil && strings.Contains(string(content), "recording-needle") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the output never reached the recording (%v)", err)
		}
		time.Sleep(20 * time.Millisecond)
	}

	var stopped daemon.RecordingStatus
	fixture.mustCall(t, "terminal.record", map[string]any{
		"terminal_id": terminal.ID, "action": "stop",
	}, &stopped)
	if stopped.Bytes == 0 {
		t.Fatal("the recording reports no bytes")
	}
	if stopped.Artifact == nil {
		t.Fatal("the recording was not attached to its task")
	}

	content, err := os.ReadFile(stopped.Path)
	if err != nil {
		t.Fatalf("read recording: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	if len(lines) < 2 {
		t.Fatalf("recording has %d lines", len(lines))
	}
	var header struct {
		Version int `json:"version"`
		Width   int `json:"width"`
		Height  int `json:"height"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &header); err != nil {
		t.Fatalf("header is not JSON: %v", err)
	}
	if header.Version != 2 || header.Width == 0 || header.Height == 0 {
		t.Fatalf("unexpected header: %+v", header)
	}
	var event []any
	if err := json.Unmarshal([]byte(lines[1]), &event); err != nil {
		t.Fatalf("event is not JSON: %v", err)
	}
	if len(event) != 3 || event[1] != "o" {
		t.Fatalf("unexpected event: %+v", event)
	}

	var snapshot daemon.Snapshot
	fixture.mustCall(t, "system.snapshot", nil, &snapshot)
	found := false
	for _, artifact := range snapshot.Artifacts {
		if artifact.Kind == "recording" && artifact.TaskID == task.ID {
			found = true
		}
	}
	if !found {
		t.Fatal("the recording artifact is not on the task")
	}

	if err := fixture.call(t, "terminal.record", map[string]any{
		"terminal_id": terminal.ID, "action": "stop",
	}, &daemon.RecordingStatus{}); err == nil {
		t.Fatal("stopping a terminal that is not recording should fail")
	}
}

func TestRecordingRefusesARestoredTerminal(t *testing.T) {
	t.Parallel()

	fixture := newTaskAgentFixture(t)
	if err := fixture.call(t, "terminal.record", map[string]any{
		"terminal_id": "term_missing", "action": "start",
	}, &daemon.RecordingStatus{}); err == nil {
		t.Fatal("recording a terminal that does not exist should fail")
	}
}
