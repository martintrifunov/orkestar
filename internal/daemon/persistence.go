package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"path/filepath"
	"sort"
	"time"

	"github.com/martintrifunov/orkestar/internal/store"
)

func (s *Server) openStore() error {
	db, err := store.Open(filepath.Join(filepath.Dir(s.socketPath), "metadata.db"))
	if err != nil {
		return err
	}
	s.store = db
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	b, err := db.Load(ctx)
	if err != nil {
		return err
	}
	if len(b) == 0 {
		return nil
	}
	var saved Snapshot
	if err := json.Unmarshal(b, &saved); err != nil {
		return fmt.Errorf("decode persisted metadata: %w", err)
	}
	for _, w := range saved.Workspaces {
		s.workspaces[w.ID] = w
	}
	for _, t := range saved.Terminals {
		if t.State == "running" {
			t.State = "interrupted"
			t.ExitError = "daemon restarted; process is no longer attached"
		}
		s.terminals[t.ID] = newTerminalSession(t, nil)
	}
	for _, a := range saved.Agents {
		if a.State != "stopped" && a.State != "crashed" {
			a.State = "interrupted"
		}
		a.AttentionReason = "daemon restarted; select resume to relaunch native session"
		s.agents[a.ID] = newAgentSession(a, nil)
	}
	s.tasks.Restore(saved.Tasks)
	s.artifacts.Restore(saved.Artifacts)
	return nil
}
func (s *Server) persist() error {
	s.persistMu.Lock()
	defer s.persistMu.Unlock()
	if s.store == nil {
		return nil
	}
	state := s.snapshot()
	state.Permissions = nil
	state.Leases = nil
	state.Adapters = nil
	b, err := json.Marshal(state)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := s.store.Save(ctx, b); err != nil {
		return fmt.Errorf("save metadata: %w", err)
	}
	return nil
}
func (s *Server) resumeAgent(ctx context.Context, raw json.RawMessage) (Agent, error) {
	var p struct {
		AgentID string `json:"agent_id"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return Agent{}, err
	}
	entry, err := s.findAgent(p.AgentID)
	if err != nil {
		return Agent{}, err
	}
	old := entry.snapshot()
	if old.State != "stopped" && old.State != "crashed" && old.State != "interrupted" {
		return Agent{}, fmt.Errorf("agent %s is still active", old.ID)
	}
	if old.NativeSessionID == "" {
		return Agent{}, fmt.Errorf("agent %s has no native session ID to resume", old.ID)
	}
	params, _ := json.Marshal(map[string]string{"workspace_id": old.WorkspaceID, "adapter": old.Adapter, "mode": old.Mode, "resume_session_id": old.NativeSessionID, "task_id": old.TaskID})
	resumed, err := s.launchAgent(ctx, params)
	if err != nil {
		return Agent{}, err
	}
	// The resume created a new agent and terminal. Forget the old ones only
	// once the launch succeeded: a failed resume must leave the record intact
	// so the user can try again, and a kept token or orphaned terminal would
	// otherwise accumulate on every successful resume.
	if orphan := s.forgetAgent(old.ID, old.TerminalID); orphan != nil {
		_ = orphan.close()
	}
	return resumed, nil
}

// autoResumeInterrupted relaunches the agent sessions that were running when
// the daemon last stopped and that reported a native session ID. It runs once,
// when the first client connects after a restart, so nothing is relaunched
// unattended and reopening the interface brings the conversations back without
// a command. A session whose adapter is gone, whose executable is missing, or
// whose launch fails is left interrupted rather than forgotten.
func (s *Server) autoResumeInterrupted() {
	s.mu.RLock()
	ids := make([]string, 0)
	for id, entry := range s.agents {
		metadata := entry.snapshot()
		if metadata.State == "interrupted" && metadata.NativeSessionID != "" {
			ids = append(ids, id)
		}
	}
	s.mu.RUnlock()
	sort.Strings(ids)

	for _, id := range ids {
		params, err := json.Marshal(map[string]string{"agent_id": id})
		if err != nil {
			continue
		}
		if _, err := s.resumeAgent(context.Background(), params); err != nil {
			log.Printf("auto-resume agent %s: %v", id, err)
			continue
		}
		log.Printf("auto-resumed agent %s", id)
	}
}
