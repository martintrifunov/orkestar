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

func fakePane(t *testing.T, id string) *embeddedTerminal {
	t.Helper()
	screen := terminal.NewScreen(20, 5)
	t.Cleanup(func() { screen.Close() })
	_, _ = screen.Write([]byte("pane-" + id))
	return &embeddedTerminal{terminalID: id, emulator: screen}
}

// assertTiled checks that the pane boxes cover the content area exactly once
// and that the rendered frame stays inside the window.
func assertTiled(t *testing.T, m Model, want int) {
	t.Helper()
	rects := m.paneRects()
	if len(rects) != want {
		t.Fatalf("got %d rects, want %d", len(rects), want)
	}
	x, y, w, h := m.contentArea()
	covered := map[[2]int]int{}
	for _, r := range rects {
		if r.width < minPaneWidth || r.height < minPaneHeight || r.x < x || r.y < y || r.x+r.width > x+w || r.y+r.height > y+h {
			t.Fatalf("rect out of bounds or too small: %+v", r)
		}
		for i := r.x; i < r.x+r.width; i++ {
			for j := r.y; j < r.y+r.height; j++ {
				covered[[2]int{i, j}]++
			}
		}
	}
	if len(covered) != w*h {
		t.Fatalf("rects cover %d cells, content area has %d", len(covered), w*h)
	}
	for cell, n := range covered {
		if n != 1 {
			t.Fatalf("cell %v covered %d times", cell, n)
		}
	}
	out := m.render()
	if lipgloss.Width(out) > m.width || lipgloss.Height(out) != m.height {
		t.Fatalf("frame is %dx%d, window is %dx%d", lipgloss.Width(out), lipgloss.Height(out), m.width, m.height)
	}
	for _, r := range rects {
		if !strings.Contains(out, "pane-"+r.terminal.terminalID) {
			t.Fatalf("pane %s not rendered", r.terminal.terminalID)
		}
	}
}

func TestSplitLayoutAndMouseFocus(t *testing.T) {
	m := Model{width: 160, height: 42}
	m.addPane(fakePane(t, "0"))
	m.insertPane(fakePane(t, "1"), m.embedded, false)
	m.insertPane(fakePane(t, "2"), m.embedded, true)
	m.insertPane(fakePane(t, "3"), m.visiblePanes()[0], true)
	assertTiled(t, m, 4)
	for _, r := range m.paneRects() {
		updated, _ := m.mouseClick(tea.MouseClickMsg{X: r.x + 2, Y: r.y + 2, Button: tea.MouseLeft})
		m = updated.(Model)
		if m.embedded != r.terminal || m.sidebarFocused {
			t.Fatal("click did not focus pane")
		}
	}
	m.width = 50
	m.height = 16
	if len(m.paneRects()) != 1 || m.paneRects()[0].terminal != m.embedded {
		t.Fatal("narrow window should show the focused pane")
	}
	if !strings.Contains(m.render(), "Enlarge window for splits") {
		t.Fatal("hidden panes were not reported")
	}
	focused := m.embedded
	m.nextPane()
	if m.embedded == focused {
		t.Fatal("cycling must reach hidden panes")
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
	for i, p := range m.visiblePanes() {
		for j := 0; j < 3; j++ {
			if i != j && strings.Contains(p.emulator.Render(), fmt.Sprintf("only%d", j)) {
				t.Fatal("input crossed pane boundary")
			}
		}
	}
	p := m.visiblePanes()[0]
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
