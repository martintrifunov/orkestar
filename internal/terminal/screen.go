package terminal

import (
	"io"
	"sync"
	"sync/atomic"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/vt"
)

// Screen confines the experimental VT implementation to this package. Read
// must be drained concurrently, BEFORE Write, Resize, Input or Paste: terminal
// queries and input write synchronously to the emulator's unbuffered pipe.
type Screen struct {
	vt      *vt.SafeEmulator
	input   io.WriteCloser
	once    sync.Once
	visible atomic.Bool
}

func NewScreen(columns, rows int) *Screen {
	e := vt.NewSafeEmulator(columns, rows)
	s := &Screen{vt: e, input: e.InputPipe().(io.WriteCloser)}
	s.visible.Store(true)
	e.SetCallbacks(vt.Callbacks{CursorVisibility: s.visible.Store})
	return s
}

func (s *Screen) Write(data []byte) (int, error) { return s.vt.Write(data) }
func (s *Screen) Read(data []byte) (int, error)  { return s.vt.Read(data) }
func (s *Screen) Render() string                 { return s.vt.Render() }
func (s *Screen) Resize(columns, rows int)       { s.vt.Resize(columns, rows) }
func (s *Screen) Input(data []byte)              { s.vt.SendText(string(data)) }
func (s *Screen) Paste(text string)              { s.vt.Paste(text) }

// Navigation encodes application-cursor mode as negotiated by the child.
func (s *Screen) Navigation(code rune, modifiers int) {
	s.vt.SendKey(uv.KeyPressEvent{Code: code, Mod: uv.KeyMod(modifiers)})
}

func (s *Screen) Cursor() (x, y int, visible bool) {
	p := s.vt.CursorPosition()
	return p.X, p.Y, s.visible.Load()
}

// Close interrupts both reads and writes without acquiring the screen lock.
// vt.Close mutates an unsynchronized flag; closing its underlying io.Pipe
// instead avoids that race and releases a Write blocked on a terminal reply.
func (s *Screen) Close() error {
	s.once.Do(func() { _ = s.input.Close() })
	return nil
}
