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
	// A recovered render panic leaves the emulator unsafe to read: say so
	// rather than returning blank output that looks like an empty pane. The
	// streaming attach path still serves a blank frame, since dropping a
	// live connection is worse than an empty repaint.
	if term.renderBroken {
		return nil, fmt.Errorf("terminal screen is no longer available")
	}
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
	if term.renderBroken {
		return nil, fmt.Errorf("terminal screen is no longer available")
	}
	text := term.text(limit)
	lines := 0
	if text != "" {
		lines = strings.Count(text, "\n") + 1
	}
	return map[string]any{
		"terminal_id": p.TerminalID,
		"text":        text,
		"lines":       lines,
		"columns":     term.metadata.Columns,
		"rows":        term.metadata.Rows,
	}, nil
}
