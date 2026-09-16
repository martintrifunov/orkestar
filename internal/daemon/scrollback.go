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
	var history []string
	if !term.renderBroken {
		if err := recoverPanic(fmt.Sprintf("terminal %s history", term.metadata.ID), func() {
			history = term.screen.History()
		}); err != nil {
			term.renderBroken = true
		}
	}
	return map[string]any{"history": history, "screen": term.frame()}, nil
}
