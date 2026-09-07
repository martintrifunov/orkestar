package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
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
	return s.launchAgent(ctx, params)
}
