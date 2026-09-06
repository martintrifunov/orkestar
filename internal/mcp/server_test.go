package mcp_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/martintrifunov/orkestar/internal/agent"
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

// stubAdapter is enough of an agent for the daemon to launch: the MCP tests
// care about which adapter and task a launch names, not about what the
// session then does.
type stubAdapter struct {
	capabilities agent.Capabilities
	sessions     *stubSessions
}

// stubSessions collects what the daemon did to the sessions this adapter
// handed out, so a test can check an agent was actually told something.
type stubSessions struct {
	mu      sync.Mutex
	prompts []string
}

func (a stubAdapter) Capabilities() agent.Capabilities { return a.capabilities }
func (a stubAdapter) Launch(context.Context, agent.LaunchOptions) (agent.Session, error) {
	return &stubSession{events: make(chan agent.LifecycleEvent, 4), shared: a.sessions}, nil
}

func (a stubAdapter) prompts() []string {
	a.sessions.mu.Lock()
	defer a.sessions.mu.Unlock()
	return append([]string(nil), a.sessions.prompts...)
}

type stubSession struct {
	events chan agent.LifecycleEvent
	shared *stubSessions
	closed bool
}

func (s *stubSession) ID() string              { return "stub" }
func (s *stubSession) NativeSessionID() string { return "stub-native" }
func (s *stubSession) State() agent.State      { return agent.StateReady }
func (s *stubSession) Prompt(_ context.Context, text string) error {
	if s.shared != nil {
		s.shared.mu.Lock()
		s.shared.prompts = append(s.shared.prompts, text)
		s.shared.mu.Unlock()
	}
	return nil
}
func (s *stubSession) Interrupt(context.Context) error     { return nil }
func (s *stubSession) Events() <-chan agent.LifecycleEvent { return s.events }
func (s *stubSession) Close() error {
	if !s.closed {
		s.closed = true
		close(s.events)
	}
	return nil
}

func startTestDaemonWithAdapters(t *testing.T, adapters ...agent.Adapter) *ipc.Client {
	t.Helper()

	socketDirectory, err := os.MkdirTemp("/tmp", "orkestar-mcp-agent-")
	if err != nil {
		t.Fatalf("create socket directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDirectory) })
	socketPath := filepath.Join(socketDirectory, "orkestar.sock")

	server := daemon.NewServer(socketPath)
	for _, adapter := range adapters {
		server.RegisterAdapter(adapter)
	}
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

type agentListResult struct {
	Agents   []daemon.Agent       `json:"agents"`
	Adapters []agent.Capabilities `json:"adapters"`
}

// An orchestrating agent has to be able to hand work to another agent, which
// means finding out what it can launch and then launching it against a task.
func TestMCPTaskStartHandsWorkToAnAgent(t *testing.T) {
	t.Parallel()

	daemonClient := startTestDaemonWithAdapters(t, stubAdapter{agent.Capabilities{Name: "stub", SupportsInteractive: true}, &stubSessions{}})
	session := connectMCP(t, daemonClient)

	workspace := callTool[daemon.Workspace](t, session, "workspace_create", map[string]any{"directory": t.TempDir()})
	task := callTool[workflow.Task](t, session, "task_create", map[string]any{
		"workspace_id": workspace.ID, "title": "hand this over", "auto_review": false,
	})

	listed := callTool[agentListResult](t, session, "agent_list", map[string]any{})
	if len(listed.Adapters) != 1 || listed.Adapters[0].Name != "stub" {
		t.Fatalf("agent_list does not report what can be launched: %#v", listed.Adapters)
	}
	if len(listed.Agents) != 0 {
		t.Fatalf("agent_list reports sessions that do not exist: %#v", listed.Agents)
	}

	// The workspace is not an argument: the task already knows it.
	launched := callTool[daemon.Agent](t, session, "task_start", map[string]any{"task_id": task.ID})
	if launched.TaskID != task.ID {
		t.Fatalf("agent is on task %q, want %q", launched.TaskID, task.ID)
	}
	if launched.WorkspaceID != workspace.ID {
		t.Fatalf("agent is in workspace %q, want %q", launched.WorkspaceID, workspace.ID)
	}
	if launched.Adapter != "stub" {
		t.Fatalf("agent adapter is %q", launched.Adapter)
	}

	after := callTool[agentListResult](t, session, "agent_list", map[string]any{})
	if len(after.Agents) != 1 || after.Agents[0].TaskID != task.ID {
		t.Fatalf("agent_list does not show the launched session: %#v", after.Agents)
	}
	tasks := callTool[struct {
		Tasks []workflow.Task `json:"tasks"`
	}](t, session, "task_list", map[string]any{})
	if tasks.Tasks[0].AssigneeAgentID != launched.ID {
		t.Fatalf("the task was not assigned: %#v", tasks.Tasks[0])
	}
}

// One call has to be a whole hand-off. An orchestrating agent cannot watch a
// session come up and type into it at the right moment, so task_start carries
// the work and the daemon delivers it when the agent is ready for it.
func TestMCPTaskStartTellsTheAgentWhatToDo(t *testing.T) {
	t.Parallel()

	adapter := stubAdapter{agent.Capabilities{Name: "stub", SupportsInteractive: true, SupportsPrompt: true}, &stubSessions{}}
	daemonClient := startTestDaemonWithAdapters(t, adapter)
	session := connectMCP(t, daemonClient)

	workspace := callTool[daemon.Workspace](t, session, "workspace_create", map[string]any{"directory": t.TempDir()})
	task := callTool[workflow.Task](t, session, "task_create", map[string]any{
		"workspace_id": workspace.ID, "title": "Fix the parser",
		"description": "it drops the last token", "auto_review": false,
	})

	callTool[daemon.Agent](t, session, "task_start", map[string]any{"task_id": task.ID})

	// The default briefing is the task itself: repeating what it says beats
	// inventing instructions the orchestrator never gave.
	got := adapter.prompts()
	if len(got) != 1 {
		t.Fatalf("prompts: %v", got)
	}
	if !strings.Contains(got[0], "Fix the parser") || !strings.Contains(got[0], "drops the last token") {
		t.Fatalf("the briefing does not carry the task: %q", got[0])
	}
}

// A caller with its own instructions sends them, and one that wants a bare
// session asks for that explicitly.
func TestMCPTaskStartHonoursAnExplicitPrompt(t *testing.T) {
	t.Parallel()

	adapter := stubAdapter{agent.Capabilities{Name: "stub", SupportsInteractive: true, SupportsPrompt: true}, &stubSessions{}}
	daemonClient := startTestDaemonWithAdapters(t, adapter)
	session := connectMCP(t, daemonClient)

	workspace := callTool[daemon.Workspace](t, session, "workspace_create", map[string]any{"directory": t.TempDir()})
	first := callTool[workflow.Task](t, session, "task_create", map[string]any{
		"workspace_id": workspace.ID, "title": "First", "auto_review": false,
	})
	second := callTool[workflow.Task](t, session, "task_create", map[string]any{
		"workspace_id": workspace.ID, "title": "Second", "auto_review": false,
	})

	callTool[daemon.Agent](t, session, "task_start", map[string]any{
		"task_id": first.ID, "prompt": "read the tests first",
	})
	callTool[daemon.Agent](t, session, "task_start", map[string]any{
		"task_id": second.ID, "prompt": "",
	})

	got := adapter.prompts()
	if len(got) != 1 || got[0] != "read the tests first" {
		t.Fatalf("prompts: %v", got)
	}
}

func TestMCPAgentPromptFollowsUp(t *testing.T) {
	t.Parallel()

	adapter := stubAdapter{agent.Capabilities{Name: "stub", SupportsInteractive: true, SupportsPrompt: true}, &stubSessions{}}
	daemonClient := startTestDaemonWithAdapters(t, adapter)
	session := connectMCP(t, daemonClient)

	workspace := callTool[daemon.Workspace](t, session, "workspace_create", map[string]any{"directory": t.TempDir()})
	task := callTool[workflow.Task](t, session, "task_create", map[string]any{
		"workspace_id": workspace.ID, "title": "hand this over", "auto_review": false,
	})
	launched := callTool[daemon.Agent](t, session, "task_start", map[string]any{
		"task_id": task.ID, "prompt": "",
	})

	callTool[map[string]string](t, session, "agent_prompt", map[string]any{
		"agent_id": launched.ID, "text": "one more thing",
	})
	if got := adapter.prompts(); len(got) != 1 || got[0] != "one more thing" {
		t.Fatalf("the follow-up did not reach the agent: %v", got)
	}

	callToolExpectError(t, session, "agent_prompt", map[string]any{
		"agent_id": "agent_missing", "text": "nobody",
	})
}

// An adapter that supports neither mode has to say so, rather than be launched
// in one it rejects and fail somewhere less legible.
func TestMCPTaskStartRejectsAnUnlaunchableAdapter(t *testing.T) {
	t.Parallel()

	daemonClient := startTestDaemonWithAdapters(t, stubAdapter{agent.Capabilities{Name: "inert"}, &stubSessions{}})
	session := connectMCP(t, daemonClient)

	workspace := callTool[daemon.Workspace](t, session, "workspace_create", map[string]any{"directory": t.TempDir()})
	task := callTool[workflow.Task](t, session, "task_create", map[string]any{
		"workspace_id": workspace.ID, "title": "nobody can take this", "auto_review": false,
	})
	callToolExpectError(t, session, "task_start", map[string]any{"task_id": task.ID, "adapter": "inert"})
}

func TestMCPTaskStartNeedsAnAdapterItCanName(t *testing.T) {
	t.Parallel()

	daemonClient := startTestDaemonWithAdapters(t,
		stubAdapter{agent.Capabilities{Name: "first", SupportsInteractive: true}, &stubSessions{}},
		stubAdapter{agent.Capabilities{Name: "second", SupportsInteractive: true}, &stubSessions{}})
	session := connectMCP(t, daemonClient)

	workspace := callTool[daemon.Workspace](t, session, "workspace_create", map[string]any{"directory": t.TempDir()})
	task := callTool[workflow.Task](t, session, "task_create", map[string]any{
		"workspace_id": workspace.ID, "title": "ambiguous", "auto_review": false,
	})

	// With more than one adapter the caller has to choose.
	callToolExpectError(t, session, "task_start", map[string]any{"task_id": task.ID})
	callToolExpectError(t, session, "task_start", map[string]any{"task_id": task.ID, "adapter": "nonexistent"})
	callToolExpectError(t, session, "task_start", map[string]any{"task_id": "task_missing", "adapter": "first"})

	launched := callTool[daemon.Agent](t, session, "task_start", map[string]any{
		"task_id": task.ID, "adapter": "second",
	})
	if launched.Adapter != "second" {
		t.Fatalf("launched %q", launched.Adapter)
	}
}

// Editing a task through MCP has the same rule as everywhere else: absent
// fields are left alone, and a dependency edit may not build a cycle.
func TestMCPTaskUpdate(t *testing.T) {
	t.Parallel()

	daemonClient := startTestDaemon(t)
	session := connectMCP(t, daemonClient)

	workspace := callTool[daemon.Workspace](t, session, "workspace_create", map[string]any{"directory": t.TempDir()})
	first := callTool[workflow.Task](t, session, "task_create", map[string]any{
		"workspace_id": workspace.ID, "title": "First", "description": "as written", "auto_review": false,
	})
	second := callTool[workflow.Task](t, session, "task_create", map[string]any{
		"workspace_id": workspace.ID, "title": "Second", "auto_review": false,
	})

	renamed := callTool[workflow.Task](t, session, "task_update", map[string]any{
		"task_id": first.ID, "title": "Renamed",
	})
	if renamed.Title != "Renamed" {
		t.Fatalf("title is %q", renamed.Title)
	}
	if renamed.Description != "as written" {
		t.Fatalf("the description was lost: %q", renamed.Description)
	}

	linked := callTool[workflow.Task](t, session, "task_update", map[string]any{
		"task_id": second.ID, "depends_on": []string{first.ID},
	})
	if len(linked.DependsOn) != 1 {
		t.Fatalf("dependencies are %v", linked.DependsOn)
	}

	// Closing the loop would leave both tasks unstartable forever.
	callToolExpectError(t, session, "task_update", map[string]any{
		"task_id": first.ID, "depends_on": []string{second.ID},
	})
}

// The whole point of the wait primitive, end to end: an agent starts work and
// blocks on the result rather than asking again and again.
func TestMCPTaskWait(t *testing.T) {
	t.Parallel()

	daemonClient := startTestDaemon(t)
	session := connectMCP(t, daemonClient)

	workspace := callTool[daemon.Workspace](t, session, "workspace_create", map[string]any{"directory": t.TempDir()})
	task := callTool[workflow.Task](t, session, "task_create", map[string]any{
		"workspace_id": workspace.ID, "title": "Ship it", "auto_review": false,
	})

	waited := make(chan workflow.Task, 1)
	go func() {
		waited <- callTool[workflow.Task](t, session, "task_wait", map[string]any{
			"task_id": task.ID, "until": "done", "timeout_seconds": 20,
		})
	}()

	select {
	case got := <-waited:
		t.Fatalf("the wait returned before anything happened: %+v", got)
	case <-time.After(200 * time.Millisecond):
	}

	callTool[workflow.Task](t, session, "task_set_status", map[string]any{
		"task_id": task.ID, "status": "done",
	})

	select {
	case got := <-waited:
		if got.Status != workflow.StatusDone {
			t.Fatalf("the wait returned %+v", got)
		}
	case <-time.After(25 * time.Second):
		t.Fatal("the wait never returned")
	}
}

// Holding a dependency, an orchestrator wants to know when the thing it is
// gating becomes startable.
func TestMCPTaskWaitForStartable(t *testing.T) {
	t.Parallel()

	daemonClient := startTestDaemon(t)
	session := connectMCP(t, daemonClient)

	workspace := callTool[daemon.Workspace](t, session, "workspace_create", map[string]any{"directory": t.TempDir()})
	blocker := callTool[workflow.Task](t, session, "task_create", map[string]any{
		"workspace_id": workspace.ID, "title": "First", "auto_review": false,
	})
	dependent := callTool[workflow.Task](t, session, "task_create", map[string]any{
		"workspace_id": workspace.ID, "title": "Second",
		"depends_on": []string{blocker.ID}, "auto_review": false,
	})

	waited := make(chan workflow.Task, 1)
	go func() {
		waited <- callTool[workflow.Task](t, session, "task_wait", map[string]any{
			"task_id": dependent.ID, "until": "startable", "timeout_seconds": 20,
		})
	}()
	select {
	case <-waited:
		t.Fatal("a blocked task reported as startable")
	case <-time.After(200 * time.Millisecond):
	}

	callTool[workflow.Task](t, session, "task_set_status", map[string]any{
		"task_id": blocker.ID, "status": "done",
	})
	select {
	case <-waited:
	case <-time.After(25 * time.Second):
		t.Fatal("clearing the dependency did not release the wait")
	}
}

// Waiting for something that can no longer happen is reported, not sat on.
func TestMCPWaitFailsRatherThanHanging(t *testing.T) {
	t.Parallel()

	daemonClient := startTestDaemon(t)
	session := connectMCP(t, daemonClient)

	workspace := callTool[daemon.Workspace](t, session, "workspace_create", map[string]any{"directory": t.TempDir()})
	task := callTool[workflow.Task](t, session, "task_create", map[string]any{
		"workspace_id": workspace.ID, "title": "Abandoned", "auto_review": false,
	})
	callTool[workflow.Task](t, session, "task_set_status", map[string]any{
		"task_id": task.ID, "status": "cancelled",
	})

	callToolExpectError(t, session, "task_wait", map[string]any{
		"task_id": task.ID, "until": "done", "timeout_seconds": 20,
	})
	callToolExpectError(t, session, "task_wait", map[string]any{
		"task_id": "task_missing", "until": "done", "timeout_seconds": 5,
	})
}

// An orchestrator that starts an agent needs to know when it is stuck.
func TestMCPAgentWait(t *testing.T) {
	t.Parallel()

	adapter := stubAdapter{agent.Capabilities{Name: "stub", SupportsInteractive: true, SupportsPrompt: true}, &stubSessions{}}
	daemonClient := startTestDaemonWithAdapters(t, adapter)
	session := connectMCP(t, daemonClient)

	workspace := callTool[daemon.Workspace](t, session, "workspace_create", map[string]any{"directory": t.TempDir()})
	task := callTool[workflow.Task](t, session, "task_create", map[string]any{
		"workspace_id": workspace.ID, "title": "Work", "auto_review": false,
	})
	launched := callTool[daemon.Agent](t, session, "task_start", map[string]any{"task_id": task.ID})

	// The stub reports ready and never moves, so a short wait for idle must
	// time out rather than return something untrue.
	callToolExpectError(t, session, "agent_wait", map[string]any{
		"agent_id": launched.ID, "until": "idle", "timeout_seconds": 1,
	})
	callToolExpectError(t, session, "agent_wait", map[string]any{
		"agent_id": launched.ID, "until": "whenever", "timeout_seconds": 5,
	})
}
