package tui

import (
	"context"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/martintrifunov/orkestar/internal/terminal"
)

type historyMsg struct {
	lines []string
	err   error
}

func (m Model) loadHistory() tea.Cmd {
	if m.embedded == nil {
		return nil
	}
	id := m.embedded.terminalID
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		var result struct {
			History []string       `json:"history"`
			Screen  terminal.Frame `json:"screen"`
		}
		err := m.client.Call(ctx, "terminal.history", map[string]string{"terminal_id": id}, &result)
		return historyMsg{append(result.History, strings.Split(result.Screen.Content, "\n")...), err}
	}
}
func (m Model) renderHistory(rows int) string {
	end := max(0, len(m.history)-m.historyOffset)
	start := max(0, end-rows)
	return strings.Join(m.history[start:end], "\n")
}
