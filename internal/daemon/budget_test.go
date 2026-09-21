package daemon_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/martintrifunov/orkestar/internal/daemon"
	"github.com/martintrifunov/orkestar/internal/workflow"
)

// launchBudgetedAgent creates a task with a budget and starts an agent on it,
// returning the agent and its session.
func launchBudgetedAgent(t *testing.T, fixture taskAgentFixture, budget map[string]any) (daemon.Agent, *controllableSession) {
	t.Helper()
	task := fixture.createTask(t, "budgeted work")
	fixture.mustCall(t, "task.setBudget", budgetWithTaskID(budget, task.ID), &workflow.Task{})
	var launched daemon.Agent
	fixture.mustCall(t, "agent.launch", map[string]any{
		"workspace_id": fixture.workspace.ID, "adapter": "fake-agent",
		"mode": "interactive", "task_id": task.ID,
	}, &launched)
	return launched, fixture.adapter.launched(t)
}

func budgetWithTaskID(budget map[string]any, taskID string) map[string]any {
	params := map[string]any{"task_id": taskID}
	for key, value := range budget {
		params[key] = value
	}
	return params
}

func agentInSnapshot(t *testing.T, fixture taskAgentFixture, agentID string) daemon.Agent {
	t.Helper()
	var snapshot daemon.Snapshot
	fixture.mustCall(t, "system.snapshot", nil, &snapshot)
	for _, candidate := range snapshot.Agents {
		if candidate.ID == agentID {
			return candidate
		}
	}
	t.Fatalf("agent %q is not in the snapshot", agentID)
	return daemon.Agent{}
}

func waitForAgentAttention(t *testing.T, fixture taskAgentFixture, agentID string) daemon.Agent {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		agent := agentInSnapshot(t, fixture, agentID)
		if agent.AttentionReason != "" {
			return agent
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("agent %q never raised attention", agentID)
	return daemon.Agent{}
}

func sendUsage(t *testing.T, fixture taskAgentFixture, agentID, token string, tokens int64) {
	t.Helper()
	var result map[string]string
	fixture.mustCall(t, "agent.hook", map[string]any{
		"agent_id": agentID, "token": token, "event": "Usage", "tokens": tokens,
	}, &result)
}

func TestUsageIsReadFromAClaudeTranscript(t *testing.T) {
	t.Parallel()

	fixture := newTaskAgentFixtureWith(t, newControllableAdapter("claude-code"))
	task := fixture.createTask(t, "transcript work")
	var launched daemon.Agent
	fixture.mustCall(t, "agent.launch", map[string]any{
		"workspace_id": fixture.workspace.ID, "adapter": "claude-code",
		"mode": "interactive", "task_id": task.ID,
	}, &launched)
	session := fixture.adapter.launched(t)

	transcript := filepath.Join(t.TempDir(), "session.jsonl")
	body := `{"type":"assistant","message":{"usage":{"input_tokens":10,"cache_read_input_tokens":100,"output_tokens":5}}}` + "\n"
	if err := os.WriteFile(transcript, []byte(body), 0o644); err != nil {
		t.Fatalf("write transcript: %v", err)
	}

	var result map[string]string
	fixture.mustCall(t, "agent.hook", map[string]any{
		"agent_id": launched.ID, "token": session.hookToken(),
		"event": "PreToolUse", "transcript_path": transcript,
	}, &result)

	agent := agentInSnapshot(t, fixture, launched.ID)
	if agent.TokensUsed != 115 {
		t.Fatalf("expected 115 tokens from the transcript, got %d", agent.TokensUsed)
	}
}

func TestTokenBudgetWarnsAndRecordsTheReason(t *testing.T) {
	t.Parallel()

	fixture := newTaskAgentFixture(t)
	launched, session := launchBudgetedAgent(t, fixture, map[string]any{
		"tokens": 100, "action": workflow.BudgetActionWarn,
	})
	sendUsage(t, fixture, launched.ID, session.hookToken(), 150)

	agent := waitForAgentAttention(t, fixture, launched.ID)
	if !strings.Contains(agent.AttentionReason, "token budget exceeded") {
		t.Fatalf("unexpected attention reason: %q", agent.AttentionReason)
	}
	if session.interrupts.Load() != 0 {
		t.Fatal("a warn budget interrupted the agent")
	}

	var snapshot daemon.Snapshot
	fixture.mustCall(t, "system.snapshot", nil, &snapshot)
	recorded := false
	for _, artifact := range snapshot.Artifacts {
		if artifact.Label == "budget exceeded" {
			recorded = true
		}
	}
	if !recorded {
		t.Fatal("the budget crossing was not recorded as an artifact")
	}
}

func TestTokenBudgetStopInterruptsTheAgent(t *testing.T) {
	t.Parallel()

	fixture := newTaskAgentFixture(t)
	launched, session := launchBudgetedAgent(t, fixture, map[string]any{
		"tokens": 100, "action": workflow.BudgetActionStop,
	})
	sendUsage(t, fixture, launched.ID, session.hookToken(), 150)

	agent := waitForAgentAttention(t, fixture, launched.ID)
	if !strings.Contains(agent.AttentionReason, "interrupted") {
		t.Fatalf("unexpected attention reason: %q", agent.AttentionReason)
	}
	if session.interrupts.Load() != 1 {
		t.Fatalf("expected one interrupt, got %d", session.interrupts.Load())
	}

	// A second hook must not interrupt again: the stop already happened.
	sendUsage(t, fixture, launched.ID, session.hookToken(), 160)
	if session.interrupts.Load() != 1 {
		t.Fatalf("the stop budget interrupted twice: %d", session.interrupts.Load())
	}
}

func TestTimeBudgetCrossesOnElapsedSessionTime(t *testing.T) {
	t.Parallel()

	fixture := newTaskAgentFixture(t)
	launched, session := launchBudgetedAgent(t, fixture, map[string]any{
		"seconds": 1, "action": workflow.BudgetActionWarn,
	})
	time.Sleep(1100 * time.Millisecond)
	sendUsage(t, fixture, launched.ID, session.hookToken(), 1)

	agent := waitForAgentAttention(t, fixture, launched.ID)
	if !strings.Contains(agent.AttentionReason, "time budget exceeded") {
		t.Fatalf("unexpected attention reason: %q", agent.AttentionReason)
	}
}

func TestBudgetIsExposedByExplain(t *testing.T) {
	t.Parallel()

	fixture := newTaskAgentFixture(t)
	launched, session := launchBudgetedAgent(t, fixture, map[string]any{
		"tokens": 100, "action": workflow.BudgetActionWarn,
	})
	sendUsage(t, fixture, launched.ID, session.hookToken(), 150)

	var explanation daemon.AgentExplanation
	fixture.mustCall(t, "agent.explain", map[string]string{"agent_id": launched.ID}, &explanation)
	if explanation.Budget == nil {
		t.Fatal("explain did not report the budget")
	}
	if explanation.Budget.TokenBudget != 100 || explanation.Budget.TokensUsed != 150 || !explanation.Budget.Exceeded {
		t.Fatalf("unexpected budget status: %+v", explanation.Budget)
	}
}
