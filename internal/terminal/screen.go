package terminal

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
)

// Screen confines the experimental VT implementation to this package. Read
// must be drained concurrently, BEFORE Write, Resize, Input or Paste: terminal
// queries and input write synchronously to the emulator's unbuffered pipe.
type Screen struct {
	mouseModes map[ansi.Mode]bool
	vt         *vt.Emulator
	guard      stringGuard
	mu         sync.Mutex
	input      io.WriteCloser
	once       sync.Once
	visible    atomic.Bool
}

func NewScreen(columns, rows int) *Screen {
	e := vt.NewEmulator(columns, rows)
	s := &Screen{mouseModes: make(map[ansi.Mode]bool), vt: e, input: e.InputPipe().(io.WriteCloser)}
	e.SetScrollbackSize(2000)
	s.visible.Store(true)
	e.SetCallbacks(vt.Callbacks{CursorVisibility: s.visible.Store,
		EnableMode: func(m ansi.Mode) {
			switch m {
			case ansi.ModeMouseX10, ansi.ModeMouseNormal, ansi.ModeMouseButtonEvent, ansi.ModeMouseAnyEvent:
				s.mouseModes[m] = true
			}
		},
		DisableMode: func(m ansi.Mode) { delete(s.mouseModes, m) },
	})
	return s
}

// Frame is a complete renderable screen, independent of VT implementation.
type Frame struct {
	Mouse         bool   `json:"mouse,omitempty"`
	Content       string `json:"content"`
	Columns       int    `json:"columns"`
	Rows          int    `json:"rows"`
	CursorX       int    `json:"cursor_x"`
	CursorY       int    `json:"cursor_y"`
	CursorVisible bool   `json:"cursor_visible"`
	Revision      uint64 `json:"revision"`
}

func (f Frame) ANSI() string {
	cursor := "\x1b[?25l"
	if f.CursorVisible {
		cursor = fmt.Sprintf("\x1b[%d;%dH\x1b[?25h", f.CursorY+1, f.CursorX+1)
	}
	return "\x1b[H\x1b[2J" + strings.ReplaceAll(f.Content, "\n", "\r\n") + cursor
}

// Write feeds output to the emulator through stringGuard, which keeps a UTF-8
// character inside a string sequence from ending it early. It reports the full
// length on success because the guard may hand the emulator fewer bytes than
// it was given, and a short write would look like an error to the caller.
func (s *Screen) Write(data []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.vt.Write(s.guard.filter(data)); err != nil {
		return 0, err
	}
	return len(data), nil
}
func (s *Screen) Read(data []byte) (int, error) { return s.vt.Read(data) }
func (s *Screen) Render() string                { return s.Frame().Content }
func (s *Screen) Resize(columns, rows int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.vt.Resize(columns, rows)
}
func (s *Screen) Input(data []byte) { s.mu.Lock(); defer s.mu.Unlock(); s.vt.SendText(string(data)) }
func (s *Screen) Paste(text string) { s.mu.Lock(); defer s.mu.Unlock(); s.vt.Paste(text) }
func (s *Screen) Navigation(code rune, modifiers int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.vt.SendKey(uv.KeyPressEvent{Code: code, Mod: uv.KeyMod(modifiers)})
}
func (s *Screen) Cursor() (int, int, bool) {
	f := s.Frame()
	return f.CursorX, f.CursorY, f.CursorVisible
}
func (s *Screen) Frame() Frame {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.vt.CursorPosition()
	return Frame{Mouse: len(s.mouseModes) > 0, Content: s.vt.Render(), Columns: s.vt.Width(), Rows: s.vt.Height(), CursorX: p.X, CursorY: p.Y, CursorVisible: s.visible.Load()}
}
func (s *Screen) History() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	lines := make([]string, s.vt.ScrollbackLen())
	for y := range lines {
		var line strings.Builder
		for x := 0; x < s.vt.Width(); x++ {
			if cell := s.vt.ScrollbackCellAt(x, y); cell != nil {
				line.WriteString(cell.Content)
			}
		}
		lines[y] = strings.TrimRight(line.String(), " ")
	}
	return lines
}

// Close interrupts both reads and writes without acquiring the screen lock.
// vt.Close mutates an unsynchronized flag; closing its underlying io.Pipe
// instead avoids that race and releases a Write blocked on a terminal reply.
func (s *Screen) Close() error {
	s.once.Do(func() { _ = s.input.Close() })
	return nil
}

func (s *Screen) Mouse(kind string, x, y, button, mod int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if x < 0 || y < 0 || x >= s.vt.Width() || y >= s.vt.Height() {
		return
	}
	m := uv.Mouse{X: x, Y: y, Button: uv.MouseButton(button), Mod: uv.KeyMod(mod)}
	switch kind {
	case "click":
		s.vt.SendMouse(uv.MouseClickEvent(m))
	case "release":
		s.vt.SendMouse(uv.MouseReleaseEvent(m))
	case "motion":
		s.vt.SendMouse(uv.MouseMotionEvent(m))
	case "wheel":
		s.vt.SendMouse(uv.MouseWheelEvent(m))
	}
}
