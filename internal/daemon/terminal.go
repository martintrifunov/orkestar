package daemon

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/martintrifunov/orkestar/internal/ipc"
	"github.com/martintrifunov/orkestar/internal/pty"
	"github.com/martintrifunov/orkestar/internal/terminal"
)

type Terminal struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	Command     []string  `json:"command"`
	Directory   string    `json:"directory"`
	State       string    `json:"state"`
	ExitError   string    `json:"exit_error,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	Columns     int       `json:"columns"`
	Rows        int       `json:"rows"`
}
type terminalEvent struct {
	Name string
	Data []byte
	// frame marks an event whose payload is the screen as it stands when the
	// event is delivered. Rendering it here rather than in Data is what keeps
	// a coalesced event cheap: output that a client never sees costs one
	// enqueue, not one render.
	frame bool
}
type terminalSubscriber struct {
	events chan terminalEvent
	screen bool
	// lastPublish is when this subscriber was last offered a frame, which
	// bounds how often one crosses a transport that charges for it.
	lastPublish time.Time
}
type terminalSession struct {
	process     *pty.Process
	screen      *terminal.Screen
	mu          sync.Mutex
	metadata    Terminal
	subscribers map[*terminalSubscriber]struct{}
	controller  *terminalSubscriber
	revision    uint64
	// rendered caches the frame for renderedAt, so subscribers sharing a
	// revision render it once between them.
	rendered   terminal.Frame
	renderedAt uint64
	done       chan struct{}
	inputDone  chan struct{}
	closeOnce  sync.Once
}

func newTerminalSession(metadata Terminal, process *pty.Process) *terminalSession {
	if metadata.Columns <= 0 {
		metadata.Columns = 80
	}
	if metadata.Rows <= 0 {
		metadata.Rows = 24
	}
	s := &terminalSession{metadata: metadata, process: process, screen: terminal.NewScreen(metadata.Columns, metadata.Rows), subscribers: make(map[*terminalSubscriber]struct{}), done: make(chan struct{}), inputDone: make(chan struct{})}
	if process != nil {
		go func() {
			defer close(s.inputDone)
			b := make([]byte, 4096)
			for {
				n, err := s.screen.Read(b)
				if n > 0 {
					if _, e := process.Write(b[:n]); e != nil {
						_ = s.screen.Close()
						return
					}
				}
				if err != nil {
					return
				}
			}
		}()
		go s.captureOutput()
	} else {
		_ = s.screen.Close()
		close(s.inputDone)
		close(s.done)
	}
	return s
}
func (s *terminalSession) snapshot() Terminal { s.mu.Lock(); defer s.mu.Unlock(); return s.metadata }

// frame renders the screen, reusing the last render while the revision has
// not moved. Callers must hold s.mu.
func (s *terminalSession) frame() terminal.Frame {
	if s.renderedAt == s.revision && s.renderedAt != 0 {
		return s.rendered
	}
	f := s.screen.Frame()
	f.Revision = s.revision
	s.rendered, s.renderedAt = f, s.revision
	return f
}

// frameEvent encodes the current screen the way subscriber wants it. Callers
// must hold s.mu.
func (s *terminalSession) frameEvent(screen bool, terminalID string) (string, []byte) {
	f := s.frame()
	if screen {
		b, _ := json.Marshal(f)
		return "terminal.screen", b
	}
	b, _ := json.Marshal(map[string]string{"terminal_id": terminalID, "data": base64.StdEncoding.EncodeToString([]byte(f.ANSI()))})
	return "terminal.output", b
}
func (s *terminalSession) subscribe(screen, observe bool) (terminal.Frame, *terminalSubscriber, func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !observe && s.controller != nil && !screen {
		return terminal.Frame{}, nil, nil, errors.New("terminal already has an attached controller")
	}
	sub := &terminalSubscriber{events: make(chan terminalEvent, 1), screen: screen}
	s.subscribers[sub] = struct{}{}
	if !observe && s.controller == nil {
		s.controller = sub
	}
	frame := s.frame()
	if s.metadata.State != "running" {
		b, _ := json.Marshal(s.metadata)
		sub.events <- terminalEvent{Name: "terminal.exit", Data: b}
	}
	detach := func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(s.subscribers, sub)
		if s.controller == sub {
			s.controller = nil
		}
		close(sub.events)
	}
	return frame, sub, detach, nil
}

// publish tells every subscriber the screen moved. It renders nothing: the
// PTY hands over one chunk per line of sustained output, and a render for each
// of them was thrown away as soon as the next chunk arrived.
// minFramePublish is the shortest gap between frames a subscriber is offered.
//
// A frame is the whole screen, not a diff: a 120x40 pane encodes to about
// 12KB and a 200x50 one to 27KB. Over a local socket that is free and the
// client's own repaint throttle is enough. Over a link with a cost — remote
// attachment, where an ssh session carries it — an agent printing steadily
// would push hundreds of kilobytes a second per pane at the rate a PTY
// produces chunks.
//
// Sixty a second is far more than a person can read and far less than a build
// log produces. Coalescing already means a subscriber that misses frames sees
// the state they left behind, so holding one back costs nothing.
const minFramePublish = 16 * time.Millisecond

func (s *terminalSession) publish() {
	s.revision++
	// Held back rather than dropped: a subscriber with a frame already waiting
	// will render the newest screen when it takes it, because the payload is
	// resolved at delivery. What this skips is the enqueue, not the change.
	now := time.Now()
	for sub := range s.subscribers {
		if !sub.lastPublish.IsZero() && now.Sub(sub.lastPublish) < minFramePublish && len(sub.events) > 0 {
			continue
		}
		name := "terminal.output"
		if sub.screen {
			name = "terminal.screen"
		}
		select {
		case <-sub.events:
		default:
		}
		sub.events <- terminalEvent{Name: name, frame: true}
		sub.lastPublish = now
	}
}
func (s *terminalSession) input(encoded string) error {
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return fmt.Errorf("decode terminal input: %w", err)
	}
	s.screen.Input(data)
	return nil
}
func (s *terminalSession) resize(columns, rows int) error {
	if columns < 1 || rows < 1 || columns > 500 || rows > 200 {
		return errors.New("terminal size must be within 1..500 columns and 1..200 rows")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.process == nil {
		return errors.New("terminal is no longer running")
	}
	if err := s.process.Resize(columns, rows); err != nil {
		return err
	}
	s.screen.Resize(columns, rows)
	s.metadata.Columns = columns
	s.metadata.Rows = rows
	s.publish()
	return nil
}

// stop ends the process and waits briefly for the output pump to record the
// final state, so the reply says what actually happened. A session restored
// after a daemon restart has no process and is marked directly.
func (s *terminalSession) stop(timeout time.Duration) Terminal {
	live := s.process != nil
	_ = s.close()
	if !live {
		s.mu.Lock()
		if !finishedState(s.metadata.State) {
			s.metadata.State = "stopped"
			s.publish()
		}
		s.mu.Unlock()
		return s.snapshot()
	}
	select {
	case <-s.done:
	case <-time.After(timeout):
	}
	return s.snapshot()
}

func (s *terminalSession) close() error {
	s.closeOnce.Do(func() {
		_ = s.screen.Close()
		if s.process != nil {
			_ = s.process.Close()
		}
	})
	return nil
}
func (s *terminalSession) captureOutput() {
	defer close(s.done)
	defer s.process.Close()
	defer func() { _ = s.screen.Close(); <-s.inputDone }()
	b := make([]byte, 32*1024)
	for {
		n, err := s.process.Read(b)
		if n > 0 {
			s.mu.Lock()
			_, _ = s.screen.Write(b[:n])
			s.publish()
			s.mu.Unlock()
		}
		if err != nil {
			s.finish(s.process.WaitError())
			return
		}
	}
}
func (s *terminalSession) finish(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.metadata.State = "stopped"
	if err != nil {
		s.metadata.State = "crashed"
		s.metadata.ExitError = err.Error()
	}
	b, _ := json.Marshal(s.metadata)
	// The receiver obtains the final frame before the exit event.
	for sub := range s.subscribers {
		select {
		case <-sub.events:
		default:
		}
		sub.events <- terminalEvent{Name: "terminal.exit", Data: b}
	}
}

func (s *Server) startTerminal(raw json.RawMessage) (Terminal, error) {
	var p struct {
		WorkspaceID string   `json:"workspace_id"`
		Command     []string `json:"command"`
		Directory   string   `json:"directory"`
		Columns     int      `json:"columns"`
		Rows        int      `json:"rows"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return Terminal{}, err
	}
	if len(p.Command) == 0 || p.Command[0] == "" {
		return Terminal{}, errors.New("terminal command is required")
	}
	s.mu.RLock()
	w, ok := s.workspaces[p.WorkspaceID]
	s.mu.RUnlock()
	if !ok {
		return Terminal{}, fmt.Errorf("workspace %q does not exist", p.WorkspaceID)
	}
	if p.Directory == "" {
		p.Directory = w.Directory
	}
	id, err := newID("term")
	if err != nil {
		return Terminal{}, err
	}
	process, err := pty.Start(pty.StartOptions{Command: p.Command[0], Arguments: p.Command[1:], Directory: p.Directory, Columns: p.Columns, Rows: p.Rows})
	if err != nil {
		return Terminal{}, err
	}
	metadata := Terminal{ID: id, WorkspaceID: p.WorkspaceID, Command: p.Command, Directory: p.Directory, State: "running", CreatedAt: time.Now().UTC(), Columns: p.Columns, Rows: p.Rows}
	session := newTerminalSession(metadata, process)
	s.mu.Lock()
	s.terminals[id] = session
	s.mu.Unlock()
	return session.snapshot(), nil
}
func (s *Server) handleTerminalAttach(conn net.Conn, scanner *bufio.Scanner, encoder *json.Encoder, request ipc.Request) {
	if request.Version != ipc.Version {
		_ = encoder.Encode(ipc.NewErrorResponse(request.ID, "unsupported_version", "unsupported protocol version"))
		return
	}
	var p struct {
		TerminalID string `json:"terminal_id"`
		Screen     bool   `json:"screen"`
		Observe    bool   `json:"observe"`
	}
	if json.Unmarshal(request.Params, &p) != nil {
		_ = encoder.Encode(ipc.NewErrorResponse(request.ID, "invalid_params", "invalid terminal attach params"))
		return
	}
	s.mu.RLock()
	session, ok := s.terminals[p.TerminalID]
	s.mu.RUnlock()
	if !ok {
		_ = encoder.Encode(ipc.NewErrorResponse(request.ID, "not_found", "terminal does not exist"))
		return
	}
	frame, sub, detach, err := session.subscribe(p.Screen, p.Observe)
	if err != nil {
		_ = encoder.Encode(ipc.NewErrorResponse(request.ID, "terminal_busy", err.Error()))
		return
	}
	defer detach()
	if s.controlReply(conn, encoder, request, session, sub, frame) != nil {
		return
	}
	readerDone := make(chan struct{})
	commands := make(chan json.RawMessage)
	go func() {
		defer close(readerDone)
		for scanner.Scan() {
			b := append(json.RawMessage(nil), scanner.Bytes()...)
			select {
			case commands <- b:
			case <-s.stop:
				return
			}
		}
	}()
	// Closing the connection releases a blocked scanner on every exit path.
	defer func() {
		_ = conn.Close()
		for {
			select {
			case <-readerDone:
				return
			case <-commands:
			}
		}
	}()
	for {
		select {
		case <-readerDone:
			return
		case <-s.stop:
			return
		case raw := <-commands:
			var c struct {
				X         int    `json:"x"`
				Y         int    `json:"y"`
				Button    int    `json:"button"`
				Kind      string `json:"kind"`
				Version   int    `json:"version"`
				Command   string `json:"command"`
				Data      string `json:"data"`
				Text      string `json:"text"`
				Code      rune   `json:"code"`
				Modifiers int    `json:"modifiers"`
				Columns   int    `json:"columns"`
				Rows      int    `json:"rows"`
			}
			if json.Unmarshal(raw, &c) != nil || c.Version != ipc.Version {
				continue
			}
			if c.Command == "detach" {
				return
			}
			session.mu.Lock()
			if c.Command == "claim" && session.controller == nil {
				session.controller = sub
			}
			control := session.controller == sub
			session.mu.Unlock()
			if c.Command == "claim" {
				b, _ := json.Marshal(map[string]bool{"controller": control})
				_ = encoder.Encode(ipc.Event{Version: ipc.Version, Event: "terminal.control", Data: b})
				continue
			}
			if !control {
				continue
			}
			switch c.Command {
			case "input":
				err = session.input(c.Data)
			case "mouse":
				session.screen.Mouse(c.Kind, c.X, c.Y, c.Button, c.Modifiers)
			case "paste":
				session.screen.Paste(c.Text)
			case "key":
				session.screen.Navigation(c.Code, c.Modifiers)
			case "resize":
				err = session.resize(c.Columns, c.Rows)
			}
			if err != nil {
				b, _ := json.Marshal(map[string]string{"message": err.Error()})
				_ = encoder.Encode(ipc.Event{Version: ipc.Version, Event: "terminal.error", Data: b})
				err = nil
			}
		case e := <-sub.events:
			data := e.Data
			if e.frame {
				session.mu.Lock()
				_, data = session.frameEvent(p.Screen, p.TerminalID)
				session.mu.Unlock()
			}
			if e.Name == "terminal.exit" {
				// The receiver obtains the final frame before the exit event.
				session.mu.Lock()
				name, b := session.frameEvent(p.Screen, p.TerminalID)
				session.mu.Unlock()
				_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
				if encoder.Encode(ipc.Event{Version: ipc.Version, Event: name, Data: b}) != nil {
					return
				}
			}
			_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
			if encoder.Encode(ipc.Event{Version: ipc.Version, Event: e.Name, Data: data}) != nil {
				return
			}
		}
	}
}
func (s *Server) controlReply(conn net.Conn, encoder *json.Encoder, request ipc.Request, session *terminalSession, sub *terminalSubscriber, frame terminal.Frame) error {
	session.mu.Lock()
	control := session.controller == sub
	session.mu.Unlock()
	response, _ := ipc.NewResponse(request.ID, map[string]any{"terminal": session.snapshot(), "replay": base64.StdEncoding.EncodeToString([]byte(frame.ANSI())), "screen": frame, "controller": control})
	_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	return encoder.Encode(response)
}
