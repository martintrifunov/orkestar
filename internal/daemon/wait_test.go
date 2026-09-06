package daemon_test

import (
	"strings"
	"testing"
	"time"

	"github.com/martintrifunov/orkestar/internal/agent"
	"github.com/martintrifunov/orkestar/internal/daemon"
	"github.com/martintrifunov/orkestar/internal/workflow"
)

// waitCall runs a wait over IPC in the background, so a test can prove it was
// still blocked before the change that releases it.
func (f taskAgentFixture) waitCall(method string, params map[string]any) (func(*testing.T) error, func(*testing.T)) {
	done := make(chan error, 1)
	go func() {
		var ignored map[string]any
		done <- f.callWithoutT(method, params, &ignored)
	}()
	collect := func(t *testing.T) error {
		t.Helper()
		select {
		case err := <-done:
			return err
		case <-time.After(20 * time.Second):
			t.Fatalf("%s never returned", method)
			return nil
		}
	}
	stillWaiting := func(t *testing.T) {
		t.Helper()
		select {
		case err := <-done:
			t.Fatalf("%s returned early: %v", method, err)
		case <-time.After(200 * time.Millisecond):
		}
	}
	return collect, stillWaiting
}

// An orchestrating agent starts work and waits for it, rather than asking
// again and again at a model turn per ask.
func TestTaskWaitOverIPC(t *testing.T) {
	t.Parallel()

	fixture := newTaskAgentFixture(t)
	task := fixture.createTask(t, "Ship it")

	collect, stillWaiting := fixture.waitCall("task.wait", map[string]any{
		"task_id": task.ID, "until": "done", "timeout_seconds": 20,
	})
	stillWaiting(t)

	var updated workflow.Task
	fixture.mustCall(t, "task.setStatus", map[string]any{"task_id": task.ID, "status": "done"}, &updated)
	if err := collect(t); err != nil {
		t.Fatalf("wait: %v", err)
	}
}

// A wait that can never be satisfied has to say so rather than hold its
// connection until the deadline.
func TestTaskWaitFailsWhenItBecomesPointless(t *testing.T) {
	t.Parallel()

	fixture := newTaskAgentFixture(t)
	task := fixture.createTask(t, "Abandoned")

	collect, stillWaiting := fixture.waitCall("task.wait", map[string]any{
		"task_id": task.ID, "until": "done", "timeout_seconds": 20,
	})
	stillWaiting(t)

	var updated workflow.Task
	fixture.mustCall(t, "task.setStatus", map[string]any{"task_id": task.ID, "status": "cancelled"}, &updated)
	err := collect(t)
	if err == nil || !strings.Contains(err.Error(), "never") {
		t.Fatalf("cancellation reported as %v", err)
	}
}

// A wait holds a connection open by design, so it needs a ceiling.
func TestWaitTimeoutIsBounded(t *testing.T) {
	t.Parallel()

	fixture := newTaskAgentFixture(t)
	task := fixture.createTask(t, "Exists")

	var ignored map[string]any
	if err := fixture.call(t, "task.wait", map[string]any{
		"task_id": task.ID, "until": "done", "timeout_seconds": 100000,
	}, &ignored); err == nil {
		t.Fatal("an unbounded wait was accepted")
	}
	if err := fixture.call(t, "task.wait", map[string]any{
		"task_id": task.ID, "until": "done", "timeout_seconds": -1,
	}, &ignored); err == nil {
		t.Fatal("a negative timeout was accepted")
	}
	// And a short one returns on its own.
	err := fixture.call(t, "task.wait", map[string]any{
		"task_id": task.ID, "until": "done", "timeout_seconds": 1,
	}, &ignored)
	if err == nil {
		t.Fatal("the wait did not time out")
	}
}

// The condition herdr named and the one an orchestrator most needs: tell me
// when the other agent genuinely cannot continue.
func TestAgentWaitForBlocked(t *testing.T) {
	t.Parallel()

	fixture := newTaskAgentFixture(t)
	var launched daemon.Agent
	fixture.mustCall(t, "agent.launch", map[string]any{
		"workspace_id": fixture.workspace.ID, "adapter": "fake-agent",
	}, &launched)
	session := fixture.adapter.launched(t)

	collect, stillWaiting := fixture.waitCall("agent.wait", map[string]any{
		"agent_id": launched.ID, "until": "blocked", "timeout_seconds": 20,
	})
	stillWaiting(t)

	// Working is not blocked.
	fixture.hook(t, session, launched.ID, "UserPromptSubmit")
	stillWaiting(t)

	// Driven natively rather than by a PermissionRequest hook, which holds its
	// own request open until someone answers it.
	session.emit(agent.StateWaitingPermission, "needs approval")
	if err := collect(t); err != nil {
		t.Fatalf("wait: %v", err)
	}
}

func TestAgentWaitForIdle(t *testing.T) {
	t.Parallel()

	fixture := newTaskAgentFixture(t)
	var launched daemon.Agent
	fixture.mustCall(t, "agent.launch", map[string]any{
		"workspace_id": fixture.workspace.ID, "adapter": "fake-agent",
	}, &launched)
	session := fixture.adapter.launched(t)

	fixture.hook(t, session, launched.ID, "UserPromptSubmit")
	collect, stillWaiting := fixture.waitCall("agent.wait", map[string]any{
		"agent_id": launched.ID, "until": "idle", "timeout_seconds": 20,
	})
	stillWaiting(t)

	fixture.hook(t, session, launched.ID, "Stop")
	if err := collect(t); err != nil {
		t.Fatalf("wait: %v", err)
	}
}

// Waiting for an agent to go idle when it has already ended would hang until
// the deadline, so it fails instead.
func TestAgentWaitFailsOnAStoppedSession(t *testing.T) {
	t.Parallel()

	fixture := newTaskAgentFixture(t)
	var launched daemon.Agent
	fixture.mustCall(t, "agent.launch", map[string]any{
		"workspace_id": fixture.workspace.ID, "adapter": "fake-agent",
	}, &launched)
	session := fixture.adapter.launched(t)

	collect, stillWaiting := fixture.waitCall("agent.wait", map[string]any{
		"agent_id": launched.ID, "until": "idle", "timeout_seconds": 20,
	})
	stillWaiting(t)

	fixture.hook(t, session, launched.ID, "SessionEnd")
	err := collect(t)
	if err == nil || !strings.Contains(err.Error(), "never") {
		t.Fatalf("a stopped session reported as %v", err)
	}
}

func TestWaitRejectsUnknownConditions(t *testing.T) {
	t.Parallel()

	fixture := newTaskAgentFixture(t)
	task := fixture.createTask(t, "Exists")

	var ignored map[string]any
	if err := fixture.call(t, "task.wait", map[string]any{
		"task_id": task.ID, "until": "whenever", "timeout_seconds": 5,
	}, &ignored); err == nil {
		t.Fatal("an unknown task condition was accepted")
	}
	if err := fixture.call(t, "agent.wait", map[string]any{
		"agent_id": "agent_missing", "until": "idle", "timeout_seconds": 5,
	}, &ignored); err == nil {
		t.Fatal("waiting on an agent that does not exist should fail")
	}
}
