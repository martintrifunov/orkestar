package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/martintrifunov/orkestar/internal/terminal"
)

func TestSplitLayoutAndMouseFocus(t *testing.T) {
	m := Model{width: 160, height: 42}
	for i := 0; i < 4; i++ {
		screen := terminal.NewScreen(20, 5)
		defer screen.Close()
		_, _ = screen.Write([]byte(fmt.Sprintf("pane-%d", i)))
		p := &embeddedTerminal{terminalID: fmt.Sprint(i), emulator: screen}
		m.panes = append(m.panes, p)
	}
	m.embedded = m.panes[0]
	for _, stacked := range []bool{false, true} {
		m.stacked = stacked
		rects := m.paneRects()
		if len(rects) != 4 {
			t.Fatal("missing panes")
		}
		out := m.render()
		if lipgloss.Width(out) > m.width || lipgloss.Height(out) > m.height {
			t.Fatal("split panes overflow window")
		}
		for _, r := range rects {
			updated, _ := m.mouseClick(tea.MouseClickMsg{X: r.x + 2, Y: r.y + 2, Button: tea.MouseLeft})
			m = updated.(Model)
			if m.embedded != r.terminal || m.sidebarFocused {
				t.Fatal("click did not focus pane")
			}
		}
	}
	m.width = 50
	m.height = 16
	if len(m.paneRects()) != 1 {
		t.Fatal("narrow window should show the focused pane")
	}
}

func TestSplitPanesIsolateInputAndKeepDetachedProcess(t *testing.T) {
	client := startEmbeddedTestDaemon(t)
	m := Model{client: client, width: 160, height: 42}
	defer func() { m.closePanes() }()
	for i := 0; i < 3; i++ {
		script := fmt.Sprintf("stty -echo; printf 'pane%d-ready\\r\\n'; while IFS= read -r line; do printf 'pane%d:%%s\\r\\n' \"$line\"; done", i, i)
		started := startEmbeddedTestTerminal(t, client, []string{"/bin/sh", "-c", script})
		msg := openEmbeddedTerminal(client, started.ID, 60, 18)().(embeddedReadyMsg)
		if msg.err != nil {
			t.Fatal(msg.err)
		}
		m.addPane(msg.terminal)
		waitForEmbeddedEvent(t, msg.terminal, fmt.Sprintf("pane%d-ready", i))
	}
	for i, r := range m.paneRects() {
		updated, _ := m.mouseClick(tea.MouseClickMsg{X: r.x + 2, Y: r.y + 2, Button: tea.MouseLeft})
		m = updated.(Model)
		updated, _ = m.Update(tea.PasteMsg{Content: fmt.Sprintf("only%d", i)})
		m = updated.(Model)
		updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		m = updated.(Model)
		waitForEmbeddedEvent(t, r.terminal, fmt.Sprintf("pane%d:only%d", i, i))
	}
	for i, p := range m.panes {
		for j := 0; j < 3; j++ {
			if i != j && strings.Contains(p.emulator.Render(), fmt.Sprintf("only%d", j)) {
				t.Fatal("input crossed pane boundary")
			}
		}
	}
	p := m.panes[0]
	id := p.terminalID
	m.removePane(p)
	deadline := time.Now().Add(2 * time.Second)
	var reopened *embeddedTerminal
	for {
		msg := openEmbeddedTerminal(client, id, 60, 18)().(embeddedReadyMsg)
		if msg.err != nil {
			t.Fatal(msg.err)
		}
		if msg.terminal.view.controls() {
			reopened = msg.terminal
			break
		}
		msg.terminal.close()
		if time.Now().After(deadline) {
			t.Fatal("detached controller was not released")
		}
		time.Sleep(time.Millisecond)
	}
	m.addPane(reopened)
	reopened.emulator.Input([]byte("after-close\r"))
	waitForEmbeddedEvent(t, reopened, "pane0:after-close")
	// History is a read-only overlay even when pasted text includes a newline.
	m.viewingHistory = true
	updated, _ := m.Update(tea.PasteMsg{Content: "forbidden\r"})
	m = updated.(Model)
	m.viewingHistory = false
	reopened.emulator.Input([]byte("after-history\r"))
	waitForEmbeddedEvent(t, reopened, "pane0:after-history")
	if strings.Contains(reopened.emulator.Render(), "forbidden") {
		t.Fatal("history overlay forwarded input")
	}
}
