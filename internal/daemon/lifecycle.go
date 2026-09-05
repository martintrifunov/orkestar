package daemon

import (
	"encoding/json"
	"fmt"
	"time"
)

// stopTimeout bounds how long a stop request waits for a process to finish
// before replying. Reaping continues in the background either way.
const stopTimeout = 5 * time.Second

// finishedStates are the states a record must be in before it can be removed.
func finishedState(state string) bool {
	switch state {
	case "stopped", "crashed", "interrupted":
		return true
	}
	return false
}

func (s *Server) findTerminal(id string) (*terminalSession, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	session, ok := s.terminals[id]
	return session, ok
}

// agentOwning reports the agent a terminal was created for, so a bridged
// terminal is not removed out from under it.
func (s *Server) agentOwning(terminalID string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for id, entry := range s.agents {
		if entry.snapshot().TerminalID == terminalID {
			return id
		}
	}
	return ""
}

// stopTerminal ends a session's process. The record stays, so the session is
// still listed as stopped and its scrollback is still readable; terminal.remove
// clears it.
func (s *Server) stopTerminal(rawParams json.RawMessage) (Terminal, error) {
	var params struct {
		TerminalID string `json:"terminal_id"`
	}
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return Terminal{}, fmt.Errorf("decode terminal stop params: %w", err)
	}
	session, ok := s.findTerminal(params.TerminalID)
	if !ok {
		return Terminal{}, fmt.Errorf("terminal %q does not exist", params.TerminalID)
	}
	return session.stop(stopTimeout), nil
}

// removeTerminal forgets a finished session. A running one has to be stopped
// first, so one keystroke cannot discard live work.
func (s *Server) removeTerminal(rawParams json.RawMessage) (map[string]string, error) {
	var params struct {
		TerminalID string `json:"terminal_id"`
	}
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return nil, fmt.Errorf("decode terminal remove params: %w", err)
	}
	session, ok := s.findTerminal(params.TerminalID)
	if !ok {
		return nil, fmt.Errorf("terminal %q does not exist", params.TerminalID)
	}
	if state := session.snapshot().State; !finishedState(state) {
		return nil, fmt.Errorf("terminal %q is %s; stop it first", params.TerminalID, state)
	}
	if agentID := s.agentOwning(params.TerminalID); agentID != "" {
		return nil, fmt.Errorf("terminal %q belongs to agent %q; remove the agent instead", params.TerminalID, agentID)
	}
	s.mu.Lock()
	delete(s.terminals, params.TerminalID)
	s.mu.Unlock()
	_ = session.close()
	return map[string]string{"status": "removed"}, nil
}

// stopAgent ends an agent session and the PTY behind it. Process exit stays
// authoritative, so the state is not written here: the reply waits briefly for
// the agent's own lifecycle to record it.
func (s *Server) stopAgent(rawParams json.RawMessage) (Agent, error) {
	var params struct {
		AgentID string `json:"agent_id"`
	}
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return Agent{}, fmt.Errorf("decode agent stop params: %w", err)
	}
	entry, err := s.findAgent(params.AgentID)
	if err != nil {
		return Agent{}, err
	}
	if session := entry.liveSession(); session != nil {
		_ = session.Close()
	}
	if terminalID := entry.snapshot().TerminalID; terminalID != "" {
		if session, ok := s.findTerminal(terminalID); ok {
			session.stop(stopTimeout)
		}
	}
	deadline := time.Now().Add(stopTimeout)
	for !finishedState(entry.snapshot().State) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	return entry.snapshot(), nil
}

// removeAgent forgets a finished agent along with the terminal record that
// belonged to it, so neither is left pointing at the other.
func (s *Server) removeAgent(rawParams json.RawMessage) (map[string]string, error) {
	var params struct {
		AgentID string `json:"agent_id"`
	}
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return nil, fmt.Errorf("decode agent remove params: %w", err)
	}
	entry, err := s.findAgent(params.AgentID)
	if err != nil {
		return nil, err
	}
	metadata := entry.snapshot()
	if !finishedState(metadata.State) {
		return nil, fmt.Errorf("agent %q is %s; stop it first", params.AgentID, metadata.State)
	}
	s.mu.Lock()
	delete(s.agents, params.AgentID)
	delete(s.hookTokens, params.AgentID)
	var orphan *terminalSession
	if metadata.TerminalID != "" {
		if session, ok := s.terminals[metadata.TerminalID]; ok {
			orphan = session
			delete(s.terminals, metadata.TerminalID)
		}
	}
	s.mu.Unlock()
	if orphan != nil {
		_ = orphan.close()
	}
	return map[string]string{"status": "removed"}, nil
}
