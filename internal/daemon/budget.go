package daemon

import (
	"context"
	"fmt"
	"time"

	"github.com/martintrifunov/orkestar/internal/usage"
	"github.com/martintrifunov/orkestar/internal/workflow"
)

// BudgetStatus is how close an agent's task is to the limit it was given. It
// is the budget half of agent.explain: a number on a row says an agent has
// spent something, this says what it was allowed and what happens next.
type BudgetStatus struct {
	TokenBudget       int64  `json:"token_budget,omitempty"`
	TokensUsed        int64  `json:"tokens_used,omitempty"`
	TimeBudgetSeconds int64  `json:"time_budget_seconds,omitempty"`
	ElapsedSeconds    int64  `json:"elapsed_seconds,omitempty"`
	Action            string `json:"action,omitempty"`
	Exceeded          bool   `json:"exceeded,omitempty"`
}

// observeUsage updates an agent's token count from what a hook carried: an
// absolute total computed by a bridge, or a transcript path the daemon reads
// itself. Failure to read is not a lifecycle failure; it leaves the count
// where it was and time remains the only limit.
func (s *Server) observeUsage(entry *agentSession, p HookInput) {
	if p.Tokens > 0 {
		entry.addUsage(p.Tokens, true)
		return
	}
	entry.mu.Lock()
	adapter := entry.metadata.Adapter
	nativeID := entry.metadata.NativeSessionID
	entry.mu.Unlock()

	format, known := usage.SourceForAdapter(adapter)
	if !known {
		return
	}
	if p.TranscriptPath != "" {
		s.readTranscript(entry, p.TranscriptPath, format)
		return
	}
	if format == "codex" && nativeID != "" {
		s.readCodexRollout(entry, nativeID)
	}
}

// readTranscript reads the usage a transcript added since the last read and
// records it. The offset only advances past complete lines, so a transcript
// still being appended to is not parsed mid-object.
func (s *Server) readTranscript(entry *agentSession, path, format string) {
	entry.mu.Lock()
	if entry.transcriptPath != path {
		entry.transcriptPath = path
		entry.transcriptOffset = 0
	}
	offset := entry.transcriptOffset
	entry.mu.Unlock()

	var (
		tokens   int64
		next     int64
		err      error
		absolute = format == "codex"
	)
	if absolute {
		tokens, next, err = usage.ReadCodex(path, offset)
	} else {
		tokens, next, err = usage.ReadClaude(path, offset)
	}
	if err != nil {
		return
	}
	if tokens > 0 {
		entry.addUsage(tokens, absolute)
	}
	entry.mu.Lock()
	if entry.transcriptPath == path {
		entry.transcriptOffset = next
	}
	entry.mu.Unlock()
}

// readCodexRollout finds a Codex session's rollout file once and then reads it
// like any other transcript. Codex hooks do not carry a transcript path, so
// the native session ID is what locates the file.
func (s *Server) readCodexRollout(entry *agentSession, nativeID string) {
	entry.mu.Lock()
	path := entry.codexRollout
	looked := entry.codexRolloutLooked
	entry.mu.Unlock()

	if path == "" && !looked {
		found, err := usage.FindCodexRollout(usage.CodexSessionsDirectory(), nativeID)
		entry.mu.Lock()
		entry.codexRolloutLooked = true
		if err == nil {
			entry.codexRollout = found
		}
		entry.mu.Unlock()
		path = found
	}
	if path != "" {
		s.readTranscript(entry, path, "codex")
	}
}

// addUsage records a token observation. An absolute total replaces a lower
// one; a per-message reading adds to the running sum.
func (a *agentSession) addUsage(tokens int64, absolute bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if absolute {
		if tokens > a.metadata.TokensUsed {
			a.metadata.TokensUsed = tokens
		}
		return
	}
	a.metadata.TokensUsed += tokens
}

// budgetStatus reports the budget an agent's task gave it, or nil when there
// is no task or no budget.
func (s *Server) budgetStatus(metadata Agent) *BudgetStatus {
	if metadata.TaskID == "" {
		return nil
	}
	task, err := s.tasks.Get(metadata.TaskID)
	if err != nil || task.TokenBudget <= 0 && task.TimeBudgetSeconds <= 0 {
		return nil
	}
	status := &BudgetStatus{
		TokenBudget:       task.TokenBudget,
		TokensUsed:        metadata.TokensUsed,
		TimeBudgetSeconds: task.TimeBudgetSeconds,
		Action:            task.BudgetAction,
	}
	if !metadata.CreatedAt.IsZero() {
		status.ElapsedSeconds = int64(time.Since(metadata.CreatedAt).Seconds())
	}
	status.Exceeded = task.TokenBudget > 0 && metadata.TokensUsed >= task.TokenBudget ||
		task.TimeBudgetSeconds > 0 && status.ElapsedSeconds >= task.TimeBudgetSeconds
	return status
}

// budgetReason describes a crossed budget in one line, for attention and for
// the artifact recorded on the task.
func budgetReason(status *BudgetStatus) string {
	if status.TokenBudget > 0 && status.TokensUsed >= status.TokenBudget {
		return fmt.Sprintf("token budget exceeded: %s of %s used",
			usage.FormatTokens(status.TokensUsed), usage.FormatTokens(status.TokenBudget))
	}
	return fmt.Sprintf("time budget exceeded: %s of %s",
		(time.Duration(status.ElapsedSeconds) * time.Second).Round(time.Minute),
		(time.Duration(status.TimeBudgetSeconds) * time.Second).Round(time.Minute))
}

// enforceBudget raises attention, and for a stop budget interrupts the agent,
// the first time a session crosses its task's limit. The flag is what keeps a
// check that runs on every hook and every tick from repeating the action.
func (s *Server) enforceBudget(entry *agentSession) {
	metadata := entry.snapshot()
	if finishedState(metadata.State) || metadata.TaskID == "" {
		return
	}
	status := s.budgetStatus(metadata)
	if status == nil || !status.Exceeded {
		return
	}
	reason := budgetReason(status)

	entry.mu.Lock()
	if entry.budgetFlagged {
		entry.mu.Unlock()
		return
	}
	entry.budgetFlagged = true
	stop := status.Action == workflow.BudgetActionStop
	if stop {
		entry.budgetStopped = true
	}
	entry.mu.Unlock()

	if stop {
		if session := entry.liveSession(); session != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = session.Interrupt(ctx)
			cancel()
			reason += "; the agent was interrupted"
		}
	}

	entry.mu.Lock()
	entry.metadata.AttentionReason = reason
	updated := entry.metadata
	entry.mu.Unlock()
	entry.publishLifecycle(updated)

	// The attention reason can be cleared by the agent's next lifecycle
	// event, so the durable record of why a session was stopped is the
	// artifact on the task.
	if _, err := s.artifacts.Add(metadata.TaskID, workflow.ArtifactLog, "budget exceeded", "", reason); err == nil {
		_ = s.persist()
	}
}

// checkAgentBudgets enforces every live agent's budget. The watchdog loop
// calls it on its tick, which is what catches a time budget even when the
// agent reports no usage at all.
func (s *Server) checkAgentBudgets() {
	s.mu.RLock()
	entries := make([]*agentSession, 0, len(s.agents))
	for _, entry := range s.agents {
		entries = append(entries, entry)
	}
	s.mu.RUnlock()
	for _, entry := range entries {
		s.enforceBudget(entry)
	}
}
