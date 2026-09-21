package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/martintrifunov/orkestar/internal/agent"
	"github.com/martintrifunov/orkestar/internal/daemon"
	"github.com/martintrifunov/orkestar/internal/ipc"
	"github.com/martintrifunov/orkestar/internal/runtimepath"
	"github.com/martintrifunov/orkestar/internal/workflow"
)

func TestParseRunArgs(t *testing.T) {
	options, err := parseRunArgs([]string{"--workspace=/tmp/x", "--agent=fixture", "--timeout=30", "--no-review"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if options.workspace != "/tmp/x" || options.agent != "fixture" || options.timeout != 30 || options.autoReview {
		t.Fatalf("unexpected options: %+v", options)
	}
	if _, err := parseRunArgs([]string{"--task=a", "--template=b"}); err == nil {
		t.Fatal("--task and --template together should be rejected")
	}
	if _, err := parseRunArgs([]string{"--timeout=soon"}); err == nil {
		t.Fatal("a non-numeric timeout should be rejected")
	}
	if _, err := parseRunArgs([]string{"--nonsense"}); err == nil {
		t.Fatal("an unknown flag should be rejected")
	}
}

func startRunTestDaemon(t *testing.T) (runtimepath.Paths, *ipc.Client) {
	t.Helper()
	directory, err := os.MkdirTemp("/tmp", "orkestar-run-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	t.Setenv("ORKESTAR_RUNTIME_DIR", directory)

	paths, err := runtimepath.Resolve()
	if err != nil {
		t.Fatal(err)
	}
	server := daemon.NewServer(paths.Socket)
	server.RegisterAdapter(agent.NewFakeAdapter(agent.Capabilities{
		Name: "fixture", SupportsInteractive: true, SupportsPrompt: true,
	}))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("daemon did not stop")
		}
	})

	client := ipc.NewClient(paths.Socket)
	deadline := time.Now().Add(5 * time.Second)
	for {
		callCtx, cancelCall := context.WithTimeout(context.Background(), 100*time.Millisecond)
		err := client.Call(callCtx, "system.ping", nil, nil)
		cancelCall()
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("daemon startup timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
	return paths, client
}

func runCall(t *testing.T, client *ipc.Client, method string, params, result any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Call(ctx, method, params, result); err != nil {
		t.Fatalf("%s: %v", method, err)
	}
}

func TestRunReportsAnAlreadyDoneTask(t *testing.T) {
	paths, client := startRunTestDaemon(t)

	var workspace daemon.Workspace
	runCall(t, client, "workspace.create", map[string]string{"directory": t.TempDir()}, &workspace)
	var task workflow.Task
	runCall(t, client, "task.create", map[string]any{
		"workspace_id": workspace.ID, "title": "already done", "auto_review": false,
	}, &task)
	runCall(t, client, "task.setStatus", map[string]any{"task_id": task.ID, "status": "done"}, &workflow.Task{})

	var output bytes.Buffer
	if err := runPipeline(paths, []string{"--task=" + task.ID, "--agent=fixture", "--timeout=30"}, &output); err != nil {
		t.Fatalf("run: %v", err)
	}
	var summary RunSummary
	if err := json.Unmarshal(output.Bytes(), &summary); err != nil {
		t.Fatalf("decode summary %q: %v", output.String(), err)
	}
	if summary.Status != "done" || len(summary.Tasks) != 1 || summary.Tasks[0].Status != workflow.StatusDone {
		t.Fatalf("unexpected summary: %+v", summary)
	}
}

func TestRunLaunchesAndWaitsForATask(t *testing.T) {
	paths, client := startRunTestDaemon(t)

	var output bytes.Buffer
	result := make(chan error, 1)
	go func() {
		result <- runPipeline(paths, []string{
			"--workspace=" + t.TempDir(), "--title=finish me", "--agent=fixture",
			"--no-review", "--timeout=30",
		}, &output)
	}()

	var taskID string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var snapshot daemon.Snapshot
		runCall(t, client, "system.snapshot", nil, &snapshot)
		for _, task := range snapshot.Tasks {
			if task.Title == "finish me" && task.AssigneeAgentID != "" {
				taskID = task.ID
			}
		}
		if taskID != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if taskID == "" {
		t.Fatal("the run never started an agent for the task")
	}
	runCall(t, client, "task.setStatus", map[string]any{"task_id": taskID, "status": "done"}, &workflow.Task{})

	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("run: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the run did not finish")
	}
	var summary RunSummary
	if err := json.Unmarshal(output.Bytes(), &summary); err != nil {
		t.Fatalf("decode summary %q: %v", output.String(), err)
	}
	if summary.Status != "done" || len(summary.Agents) != 1 {
		t.Fatalf("unexpected summary: %+v", summary)
	}
}

func TestRunFailsWhenTheTaskIsCancelled(t *testing.T) {
	paths, client := startRunTestDaemon(t)

	var output bytes.Buffer
	result := make(chan error, 1)
	go func() {
		result <- runPipeline(paths, []string{
			"--workspace=" + t.TempDir(), "--title=cancel me", "--agent=fixture",
			"--no-review", "--timeout=30",
		}, &output)
	}()

	var taskID string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var snapshot daemon.Snapshot
		runCall(t, client, "system.snapshot", nil, &snapshot)
		for _, task := range snapshot.Tasks {
			if task.Title == "cancel me" {
				taskID = task.ID
			}
		}
		if taskID != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if taskID == "" {
		t.Fatal("the run never created the task")
	}
	runCall(t, client, "task.setStatus", map[string]any{"task_id": taskID, "status": "cancelled"}, &workflow.Task{})

	select {
	case err := <-result:
		if err == nil {
			t.Fatal("a cancelled run should fail")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the run did not finish")
	}
	var summary RunSummary
	if err := json.Unmarshal(output.Bytes(), &summary); err != nil {
		t.Fatalf("decode summary %q: %v", output.String(), err)
	}
	if summary.Status != "cancelled" {
		t.Fatalf("unexpected summary: %+v", summary)
	}
}

func TestRunRejectsWaitingTemplateTasks(t *testing.T) {
	paths, _ := startRunTestDaemon(t)

	directory := t.TempDir()
	templates := filepath.Join(directory, ".orkestar", "templates")
	if err := os.MkdirAll(templates, 0o755); err != nil {
		t.Fatal(err)
	}
	body := `{"name":"manual","tasks":[
		{"key":"first","title":"First","agent":"fixture","auto_review":false},
		{"key":"second","title":"Second","agent":"fixture","auto_review":false,"depends_on":["first"]}
	]}`
	if err := os.WriteFile(filepath.Join(templates, "manual.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	err := runPipeline(paths, []string{
		"--workspace=" + directory, "--template=manual", "--agent=fixture", "--timeout=30",
	}, &output)
	if err == nil {
		t.Fatal("a template with waiting tasks should fail a headless run")
	}
}
