package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/martintrifunov/orkestar/internal/files"
)

func TestDocumentScrollingDoesNotBounceAtEdges(t *testing.T) {
	for _, kind := range []string{"editor", "diff"} {
		t.Run(kind, func(t *testing.T) {
			m := Model{width: 120, height: 30}
			var top, maxTop func() int
			if kind == "editor" {
				e := newTextEditor(&files.Document{Text: strings.Repeat("line\n", 100)})
				p := m.localPane("Editor", t.TempDir(), e)
				p.editor = e
				e.cursor = len(e.text)
				e.reveal()
				top = func() int { return e.top }
				maxTop = e.maxTop
			} else {
				r := &reviewPane{files: []reviewFile{{path: "file"}}, diff: strings.Repeat("+line\n", 100)}
				p := m.localPane("Diff", t.TempDir(), r)
				p.review = r
				top = func() int { return r.top }
				maxTop = r.maxTop
			}
			rect := m.paneRects()[0]
			wheel := func(button tea.MouseButton) {
				updated, _ := m.Update(tea.MouseWheelMsg{X: rect.x + 5, Y: rect.y + 5, Button: button})
				m = updated.(Model)
			}
			for i := 0; i < 60; i++ {
				wheel(tea.MouseWheelUp)
			}
			if top() != 0 {
				t.Fatal("did not reach top")
			}
			for i := 0; i < 20; i++ {
				wheel(tea.MouseWheelLeft)
				wheel(tea.MouseWheelRight)
				wheel(tea.MouseWheelUp)
				m.render()
				m.resizePanes()
				m.render()
				if top() != 0 {
					t.Fatal("horizontal events or resize moved viewport away from top")
				}
			}
			for i := 0; i < 60; i++ {
				wheel(tea.MouseWheelDown)
			}
			if top() != maxTop() {
				t.Fatalf("bottom not clamped to viewport: %d, want %d", top(), maxTop())
			}
			before := top()
			for i := 0; i < 10; i++ {
				m.render()
			}
			if top() != before {
				t.Fatal("render mutated scroll state")
			}
		})
	}
}
func TestMouseMotionWithoutHeldButtonDoesNotScrollEditor(t *testing.T) {
	e := newTextEditor(&files.Document{Text: strings.Repeat("line\n", 100)})
	m := Model{width: 120, height: 30}
	p := m.localPane("Editor", t.TempDir(), e)
	p.editor = e
	e.top = 25
	e.dragging = true
	r := m.paneRects()[0]
	updated, _ := m.Update(tea.MouseMotionMsg{X: r.x + 8, Y: r.y + 5, Button: tea.MouseNone})
	m = updated.(Model)
	if e.dragging || e.top != 25 {
		t.Fatal("stale drag moved viewport")
	}
}
func TestPaneRenderingNeverEmitsTabs(t *testing.T) {
	m := Model{width: 120, height: 30}
	r := &reviewPane{files: []reviewFile{{path: "file"}}, diff: "@@ -1,2 +1,2 @@\n \t\treturn m, m.openDocument(p.root, r.files[r.selected].path)\n+\tnew\n"}
	p := m.localPane("Diff", t.TempDir(), r)
	p.review = r
	if strings.Contains(r.Render(), "\t") {
		t.Fatal("review rendered raw tabs; the terminal would wrap the line")
	}
	if got := fitPane("a\tb\n", 10, 2); strings.Contains(got, "\t") {
		t.Fatalf("fitPane kept a tab: %q", got)
	}
	for _, line := range strings.Split(ansi.Strip(m.renderPanes()), "\n") {
		if ansi.StringWidth(line) > m.width || strings.Contains(line, "\t") {
			t.Fatalf("pane row exceeds terminal width: %q", line)
		}
	}
}
