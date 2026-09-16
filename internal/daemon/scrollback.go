package daemon

import (
	"encoding/json"
	"fmt"
	"strings"
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
	return map[string]any{"history": term.history(), "screen": term.frame()}, nil
}

// maxReadLines caps terminal.read at the screen package's scrollback depth,
// and defaultReadLines is what a caller gets without asking for a count.
const (
	maxReadLines     = 2000
	defaultReadLines = 200
)

// terminalRead returns a terminal's recent output as plain text: scrollback
// followed by the visible screen, trimmed and bounded to the last N lines.
// It is the read half of the agent-native control surface: a script or agent
// can see what a pane is showing without attaching to it.
func (s *Server) terminalRead(raw json.RawMessage) (map[string]any, error) {
	var p struct {
		TerminalID string `json:"terminal_id"`
		Lines      int    `json:"lines"`
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
	limit := p.Lines
	if limit <= 0 {
		limit = defaultReadLines
	}
	if limit > maxReadLines {
		limit = maxReadLines
	}

	term.mu.Lock()
	defer term.mu.Unlock()
	lines := append(term.history(), strings.Split(term.frame().Content, "\n")...)
	// Trailing blank rows are screen padding rather than output, and a caller
	// asking for the last N lines should not have them counted.
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " ")
	}
	if len(lines) > limit {
		lines = lines[len(lines)-limit:]
	}
	return map[string]any{
		"terminal_id": p.TerminalID,
		"text":        strings.Join(lines, "\n"),
		"lines":       len(lines),
		"columns":     term.metadata.Columns,
		"rows":        term.metadata.Rows,
	}, nil
}
