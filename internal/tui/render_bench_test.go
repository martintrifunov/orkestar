package tui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/martintrifunov/orkestar/internal/terminal"
)

// staticScreen stands in for an attached pane so the benchmark measures the
// client's own rendering rather than an emulator or a daemon connection.
type staticScreen struct{ content string }

func (s *staticScreen) Render() string           { return s.content }
func (s *staticScreen) Cursor() (int, int, bool) { return 0, 0, true }
func (s *staticScreen) Resize(int, int)          {}
func (s *staticScreen) Input([]byte)             {}
func (s *staticScreen) Paste(string)             {}
func (s *staticScreen) Navigation(rune, int)     {}
func (s *staticScreen) Close() error             { return nil }

// screenContent is a frame of the coloured, box-drawn output an agent CLI
// produces, which is the expensive kind to measure against.
func screenContent(columns, rows int) string {
	screen := terminal.NewScreen(columns, rows)
	for row := range rows {
		body := strings.Repeat("─", max(0, columns-24))
		screen.Write(fmt.Appendf(nil, "\x1b[3%dm│ %04d %s │\x1b[m\r\n", row%7+1, row, body))
	}
	return screen.Render()
}

// BenchmarkClientRender measures one full-frame draw of the whole TUI, which
// is what sustained output makes the client repeat for every frame the daemon
// delivers.
func BenchmarkClientRender(b *testing.B) {
	for _, panes := range []int{1, 2, 4} {
		b.Run(fmt.Sprintf("panes=%d", panes), func(b *testing.B) {
			model := Model{width: 160, height: 48}
			for i := range panes {
				pane := &embeddedTerminal{terminalID: fmt.Sprintf("t%d", i), title: "claude", emulator: &staticScreen{}, done: make(chan struct{})}
				model.layout = model.layout.insert(model.embedded, pane, i%2 == 1)
				model.embedded = pane
			}
			for _, pane := range model.visiblePanes() {
				for _, rect := range model.paneRects() {
					if rect.terminal == pane {
						pane.emulator = &staticScreen{content: screenContent(max(1, rect.width-4), max(1, rect.height-2))}
					}
				}
			}
			b.ReportAllocs()
			for b.Loop() {
				_ = model.render()
			}
		})
	}
}
