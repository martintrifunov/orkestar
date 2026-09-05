package tui

import (
	"sync"

	"github.com/martintrifunov/orkestar/internal/ipc"
	"github.com/martintrifunov/orkestar/internal/terminal"
)

type paneScreen interface {
	Render() string
	Cursor() (int, int, bool)
	Resize(int, int)
	Input([]byte)
	Paste(string)
	Navigation(rune, int)
	Close() error
}
type remoteScreen struct {
	mu         sync.Mutex
	stream     *ipc.Stream
	frame      terminal.Frame
	controller bool
}

func (r *remoteScreen) apply(f terminal.Frame) { r.mu.Lock(); defer r.mu.Unlock(); r.frame = f }
func (r *remoteScreen) controls() bool         { r.mu.Lock(); defer r.mu.Unlock(); return r.controller }
func (r *remoteScreen) Render() string         { r.mu.Lock(); defer r.mu.Unlock(); return r.frame.Content }
func (r *remoteScreen) Cursor() (int, int, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.frame.CursorX, r.frame.CursorY, r.frame.CursorVisible && r.controller
}
func (r *remoteScreen) Resize(int, int) {}
func (r *remoteScreen) Input(b []byte) {
	if r.controls() {
		_ = sendEmbeddedInput(r.stream, b)
	}
}
func (r *remoteScreen) Paste(text string) {
	if r.controls() {
		_ = r.stream.Send(map[string]any{"version": ipc.Version, "command": "paste", "text": text})
	}
}
func (r *remoteScreen) Navigation(code rune, mod int) {
	if r.controls() {
		_ = r.stream.Send(map[string]any{"version": ipc.Version, "command": "key", "code": code, "modifiers": mod})
	}
}
func (r *remoteScreen) Close() error { return nil }

func (r *remoteScreen) mouseEnabled() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.frame.Mouse && r.controller
}
