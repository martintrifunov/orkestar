package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/martintrifunov/orkestar/internal/store"
)

// paneHistoryFile holds the bounded text of terminals across a restart. It
// sits beside the metadata database and is treated like terminal history:
// output can hold secrets, so it is only written when opt-in pane history is
// on.
const paneHistoryFile = "pane-history.json"

func (s *Server) paneHistoryPath() string {
	return filepath.Join(filepath.Dir(s.socketPath), paneHistoryFile)
}

// loadPaneHistory seeds restored terminals with the text they had when the
// daemon last stopped. Only terminals that are still known are given history.
func (s *Server) loadPaneHistory() {
	if !s.paneHistory {
		// Turning the feature off should also remove what it left on disk:
		// pane output can hold secrets, and the promise is that it is only
		// written while opt-in.
		_ = os.Remove(s.paneHistoryPath())
		return
	}
	encoded, err := os.ReadFile(s.paneHistoryPath())
	if err != nil {
		return
	}
	var saved map[string][]string
	if json.Unmarshal(encoded, &saved) != nil {
		return
	}
	s.mu.Lock()
	for id, lines := range saved {
		if session, ok := s.terminals[id]; ok {
			session.mu.Lock()
			session.restoredHistory = lines
			session.mu.Unlock()
		}
	}
	s.mu.Unlock()
}

// savePaneHistory writes every terminal's bounded text. A running terminal has
// its live screen; a restored one keeps what it came back with, so a second
// restart does not lose it.
func (s *Server) savePaneHistory() {
	if !s.paneHistory {
		return
	}
	s.mu.RLock()
	saved := make(map[string][]string, len(s.terminals))
	for id, session := range s.terminals {
		session.mu.Lock()
		// The whole bounded text, not just the scrollback: a short session's
		// output may still be on the visible screen.
		text := session.text(maxReadLines)
		session.mu.Unlock()
		if strings.TrimSpace(text) == "" {
			continue
		}
		saved[id] = strings.Split(text, "\n")
	}
	s.mu.RUnlock()

	path := s.paneHistoryPath()
	if len(saved) == 0 {
		_ = os.Remove(path)
		return
	}
	encoded, err := json.Marshal(saved)
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	_ = os.WriteFile(path, encoded, 0o600)
}

func (s *Server) openStore() error {
	metadataPath := filepath.Join(filepath.Dir(s.socketPath), "metadata.db")
	db, err := store.Open(metadataPath)
	if err != nil {
		return err
	}
	s.store = db
	// Remove any pane-history file left by an earlier opt-in before the early
	// returns below, so disabling the feature always clears it even when the
	// metadata blob is empty or quarantined.
	if !s.paneHistory {
		_ = os.Remove(s.paneHistoryPath())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	b, err := db.Load(ctx)
	if err != nil {
		// A metadata row the daemon cannot read must not brick it: a version it
		// does not understand or a corrupt blob would otherwise leave no way
		// back in, since reset needs a running daemon. Move it aside and start
		// empty, leaving the old database for recovery.
		log.Printf("metadata is unreadable (%v); moving it aside and starting empty", err)
		return s.quarantineStore(metadataPath)
	}
	if len(b) == 0 {
		return nil
	}
	var saved Snapshot
	if err := json.Unmarshal(b, &saved); err != nil {
		log.Printf("metadata is unreadable (%v); moving it aside and starting empty", err)
		return s.quarantineStore(metadataPath)
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
			// Clear any attention reason saved before the restart; only an
			// interrupted agent with somewhere to resume to gets the notice.
			a.AttentionReason = ""
			if a.NativeSessionID != "" {
				a.AttentionReason = "daemon restarted; select resume to relaunch native session"
			}
		}
		s.agents[a.ID] = newAgentSession(a, nil)
	}
	s.tasks.Restore(saved.Tasks)
	s.artifacts.Restore(saved.Artifacts)
	s.loadPaneHistory()
	return nil
}

// quarantineStore moves an unreadable metadata database aside and opens a
// fresh one, so the daemon can start and be reset rather than failing to
// serve. The old file is kept as metadata.db.corrupt; nothing is deleted.
func (s *Server) quarantineStore(path string) error {
	if s.store != nil {
		_ = s.store.Close()
		s.store = nil
	}
	backup := path + ".corrupt"
	_ = os.Remove(backup)
	if err := os.Rename(path, backup); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("quarantine metadata database: %w", err)
	}
	_ = os.Remove(path + "-wal")
	_ = os.Remove(path + "-shm")
	reopened, err := store.Open(path)
	if err != nil {
		return err
	}
	s.store = reopened
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
	// Only one resume may run for an agent: a manual resume and an automatic
	// one could otherwise both launch a session from the same interrupted
	// record.
	if !entry.beginResume() {
		return Agent{}, fmt.Errorf("agent %s is already being resumed", old.ID)
	}
	defer entry.endResume()
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

	// The same exclusion a mutation request takes, so an auto-resume cannot
	// interleave with a reset that would clear the board underneath it.
	s.mutationMu.RLock()
	defer s.mutationMu.RUnlock()
	for _, id := range ids {
		params, err := json.Marshal(map[string]string{"agent_id": id})
		if err != nil {
			continue
		}
		if _, err := s.resumeAgent(context.Background(), params); err != nil {
			log.Printf("auto-resume agent %s: %v", id, err)
			continue
		}
		// resumeAgent runs outside handleRequest here, so nothing persists
		// the new agent and the forgotten old one. Without this a second
		// crash before the next mutating call resurrects the old
		// interrupted record and loses the resumed session.
		if err := s.persist(); err != nil {
			log.Printf("auto-resume agent %s: save metadata: %v", id, err)
		}
		log.Printf("auto-resumed agent %s", id)
	}
}
