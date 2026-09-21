package daemon

import (
	"fmt"
	"time"

	"github.com/martintrifunov/orkestar/internal/agent"
)

// DefaultAgentWatchdog is how long an agent session may stay in a working
// state without a lifecycle change before the daemon raises attention.
const DefaultAgentWatchdog = 30 * time.Minute

// watchdogInterval is how often sessions are checked. It is well under the
// default limit so the notice lands close to when the limit is crossed.
const watchdogInterval = time.Minute

// SetAgentWatchdog sets the working-state limit; zero or less disables the
// watchdog. Call before Serve.
func (s *Server) SetAgentWatchdog(limit time.Duration) {
	if limit < 0 {
		limit = 0
	}
	s.watchdogLimit = limit
}

func (s *Server) watchdogLoop() {
	ticker := time.NewTicker(watchdogInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
			s.checkAgentDurations()
			s.checkAgentBudgets()
		}
	}
}

// checkAgentDurations flags agent sessions that have been working for far
// longer than a turn normally takes. This is the shape of the 2026-09-06
// incident: one turn quietly grew to millions of tokens and most of a
// rate-limit window before anyone noticed. Orkestar cannot see token usage,
// but it can see time, and an attention reason on the row is the same signal
// a crash raises.
func (s *Server) checkAgentDurations() {
	if s.watchdogLimit <= 0 {
		return
	}
	now := time.Now()
	type flagged struct {
		entry    *agentSession
		metadata Agent
	}
	var notify []flagged

	s.mu.RLock()
	for _, entry := range s.agents {
		entry.mu.Lock()
		if entry.metadata.State != string(agent.StateWorking) || entry.workingSince.IsZero() {
			entry.mu.Unlock()
			continue
		}
		elapsed := now.Sub(entry.workingSince)
		if elapsed < s.watchdogLimit || entry.metadata.AttentionReason != "" {
			entry.mu.Unlock()
			continue
		}
		entry.metadata.AttentionReason = fmt.Sprintf("working for %s without a state change", elapsed.Round(time.Minute))
		metadata := entry.metadata
		entry.mu.Unlock()
		notify = append(notify, flagged{entry: entry, metadata: metadata})
	}
	s.mu.RUnlock()

	for _, flagged := range notify {
		flagged.entry.publishLifecycle(flagged.metadata)
	}
	if len(notify) > 0 {
		_ = s.persist()
	}
}
