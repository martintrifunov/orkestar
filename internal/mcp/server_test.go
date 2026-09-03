package mcp_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/martintrifunov/orkestar/internal/daemon"
	"github.com/martintrifunov/orkestar/internal/ipc"
	orkestarmcp "github.com/martintrifunov/orkestar/internal/mcp"
	"github.com/martintrifunov/orkestar/internal/workflow"
)

func startTestDaemon(t *testing.T) *ipc.Client {
	t.Helper()

	socketDirectory, err := os.MkdirTemp("/tmp", "orkestar-mcp-test-")
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
			t.Errorf("daemon shutdown: %v", err)
		}
	})

	client := ipc.NewClient(socketPath)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		pingCtx, pingCancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		var result map[string]string
		err := client.Call(pingCtx, "system.ping", nil, &result)
		pingCancel()
		if err == nil {
			return client
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("daemon did not become ready")
	return nil
}

func connectMCP(t *testing.T, daemonClient *ipc.Client) *sdk.ClientSession {
	t.Helper()

	server := orkestarmcp.NewServer(daemonClient)
	client := sdk.NewClient(&sdk.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)

	serverTransport, clientTransport := sdk.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := server.Connect(ctx, serverTransport, nil); err != nil {
		t.Fatalf("connect MCP server: %v", err)
	}
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("connect MCP client: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func callTool[Out any](t *testing.T, session *sdk.ClientSession, name string, arguments any) Out {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := session.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		t.Fatalf("call tool %q: %v", name, err)
	}
	if result.IsError {
		t.Fatalf("tool %q returned an error result: %#v", name, result.Content)
	}

	var out Out
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content for %q: %v", name, err)
	}
	if err := json.Unmarshal(encoded, &out); err != nil {
		t.Fatalf("decode structured content for %q: %v", name, err)
	}
	return out
}

func callToolExpectError(t *testing.T, session *sdk.ClientSession, name string, arguments any) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := session.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		return
	}
	if !result.IsError {
		t.Fatalf("expected tool %q to report an error, got %#v", name, result)
	}
}

func TestMCPServerWorkspaceAndTaskLifecycle(t *testing.T) {
	t.Parallel()

	daemonClient := startTestDaemon(t)
	session := connectMCP(t, daemonClient)

	temporaryDirectory := t.TempDir()
	workspace := callTool[daemon.Workspace](t, session, "workspace_create", map[string]any{
		"directory": temporaryDirectory,
	})
	if workspace.ID == "" || workspace.Directory == "" {
		t.Fatalf("unexpected workspace: %#v", workspace)
	}

	dependency := callTool[workflow.Task](t, session, "task_create", map[string]any{
		"workspace_id": workspace.ID,
		"title":        "write tests",
	})
	if dependency.Status != workflow.StatusPending {
		t.Fatalf("unexpected dependency status: %q", dependency.Status)
	}

	task := callTool[workflow.Task](t, session, "task_create", map[string]any{
		"workspace_id": workspace.ID,
		"title":        "ship feature",
		"depends_on":   []string{dependency.ID},
	})
	if !task.AutoReview {
		t.Fatalf("expected auto_review to default true: %#v", task)
	}

	callToolExpectError(t, session, "task_set_status", map[string]any{
		"task_id": task.ID,
		"status":  string(workflow.StatusInProgress),
	})

	completedDependency := callTool[workflow.Task](t, session, "task_set_status", map[string]any{
		"task_id": dependency.ID,
		"status":  string(workflow.StatusDone),
	})
	if completedDependency.Status != workflow.StatusDone {
		t.Fatalf("unexpected dependency status: %#v", completedDependency)
	}

	inProgress := callTool[workflow.Task](t, session, "task_set_status", map[string]any{
		"task_id": task.ID,
		"status":  string(workflow.StatusInProgress),
	})
	if inProgress.Status != workflow.StatusInProgress {
		t.Fatalf("unexpected task status: %#v", inProgress)
	}

	assigned := callTool[workflow.Task](t, session, "task_assign", map[string]any{
		"task_id":  task.ID,
		"agent_id": "agent_1",
	})
	if assigned.AssigneeAgentID != "agent_1" {
		t.Fatalf("unexpected assignee: %#v", assigned)
	}

	listed := callTool[struct {
		Tasks []workflow.Task `json:"tasks"`
	}](t, session, "task_list", map[string]any{"workspace_id": workspace.ID})
	if len(listed.Tasks) != 2 {
		t.Fatalf("unexpected task list: %#v", listed.Tasks)
	}

	lease := callTool[workflow.Lease](t, session, "resource_acquire", map[string]any{
		"resource":    "unreal-editor",
		"holder_id":   "agent_1",
		"mode":        string(workflow.LeaseExclusive),
		"duration_ms": 60000,
	})
	if lease.Resource != "unreal-editor" {
		t.Fatalf("unexpected lease: %#v", lease)
	}
	callToolExpectError(t, session, "resource_acquire", map[string]any{
		"resource":  "unreal-editor",
		"holder_id": "agent_2",
		"mode":      string(workflow.LeaseExclusive),
	})
	released := callTool[map[string]string](t, session, "resource_release", map[string]any{
		"resource": "unreal-editor",
		"lease_id": lease.ID,
	})
	if released["status"] != "released" {
		t.Fatalf("unexpected release status: %#v", released)
	}

	artifact := callTool[workflow.Artifact](t, session, "artifact_create", map[string]any{
		"task_id": task.ID,
		"kind":    string(workflow.ArtifactLog),
		"label":   "build output",
		"content": "build succeeded",
	})
	if artifact.TaskID != task.ID || artifact.Kind != workflow.ArtifactLog {
		t.Fatalf("unexpected artifact: %#v", artifact)
	}
}
