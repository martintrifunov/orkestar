package daemon

import (
	"encoding/json"
	"fmt"
)

func (s *Server) terminalHistory(raw json.RawMessage) (map[string]any, error) {
	var p struct {
		TerminalID string `json:"terminal_id"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	s.mu.RLock()
	term := s.terminals[p.TerminalID]
	s.mu.RUnlock()
	if term == nil {
		return nil, fmt.Errorf("unknown terminal")
	}
	term.mu.Lock()
	defer term.mu.Unlock()
	return map[string]any{"history": term.screen.History(), "screen": term.frame()}, nil
}
