package daemon

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
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
	// renderBroken records that a render panic was recovered. The emulator's
	// buffer is then in an unknown state, so every later screen read is
	// skipped rather than risking another panic on corrupt data.
	renderBroken bool
	// restoredHistory is the bounded text a terminal came back with after a
	// daemon restart, when opt-in pane history is enabled. A restored terminal
	// has no process and no live screen, so this is all it can show.
	restoredHistory []string
	// detect, when set, infers an agent lifecycle state from the pane's text.
	// It is how a manifest adapter without hooks reports working or blocked.
	detect     func(text string)
	lastDetect time.Time
}

// detectInterval bounds how often the screen is scanned for a detected state.
// PTY output arrives in many small chunks, and scanning the whole screen for
// each one would cost far more than the answer is worth.
const detectInterval = 250 * time.Millisecond

func newTerminalSession(metadata Terminal, process *pty.Process) *terminalSession {
	if metadata.Columns <= 0 {
		metadata.Columns = 80
	}
	if metadata.Rows <= 0 {
		metadata.Rows = 24
	}
	s := &terminalSession{metadata: metadata, process: process, screen: terminal.NewScreen(metadata.Columns, metadata.Rows), subscribers: make(map[*terminalSubscriber]struct{}), done: make(chan struct{}), inputDone: make(chan struct{})}
	if process != nil {
		go guard("terminal.input-forward", func() {
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
		})
		go guard("terminal.capture-output", s.captureOutput)
	} else {
		_ = s.screen.Close()
		close(s.inputDone)
		close(s.done)
	}
	return s
}
func (s *terminalSession) snapshot() Terminal { s.mu.Lock(); defer s.mu.Unlock(); return s.metadata }

// frame renders the screen, reusing the last render while the revision has
// not moved. Callers must hold s.mu. Once a render panic has been recovered
// the emulator is left alone and a blank frame of the right size is served,
// so a corrupt screen cannot take a connection down with it.
func (s *terminalSession) frame() terminal.Frame {
	if s.renderedAt == s.revision && s.renderedAt != 0 {
		return s.rendered
	}
	if s.process == nil {
		f := terminal.Frame{Columns: s.metadata.Columns, Rows: s.metadata.Rows, Revision: s.revision}
		s.rendered, s.renderedAt = f, s.revision
		return f
	}
	f := terminal.Frame{Columns: s.metadata.Columns, Rows: s.metadata.Rows}
	if !s.renderBroken {
		if err := recoverPanic(fmt.Sprintf("terminal %s frame", s.metadata.ID), func() {
			f = s.screen.Frame()
		}); err != nil {
			s.renderBroken = true
			f = terminal.Frame{Columns: s.metadata.Columns, Rows: s.metadata.Rows}
		}
	}
	f.Revision = s.revision
	s.rendered, s.renderedAt = f, s.revision
	return f
}

// history returns the scrollback, or nil once a recovered render panic has
// made the emulator unsafe to read. Callers must hold s.mu.
func (s *terminalSession) history() []string {
	if s.process == nil {
		return s.restoredHistory
	}
	if s.renderBroken {
		return nil
	}
	var lines []string
	if err := recoverPanic(fmt.Sprintf("terminal %s history", s.metadata.ID), func() {
		lines = s.screen.History()
	}); err != nil {
		s.renderBroken = true
	}
	return lines
}

// text returns bounded plain-text output: scrollback then the visible screen,
// trailing padding removed and at most limit lines. Callers must hold s.mu.
func (s *terminalSession) text(limit int) string {
	lines := append(s.history(), strings.Split(s.frame().Content, "\n")...)
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " ")
	}
	if len(lines) > limit {
		lines = lines[len(lines)-limit:]
	}
	return strings.Join(lines, "\n")
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
	if s.renderBroken {
		return errors.New("terminal screen is no longer available")
	}
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
			if crashErr := s.renderOutput(b[:n]); crashErr != nil {
				s.finish(crashErr)
				return
			}
			s.runDetection()
		}
		if err != nil {
			s.finish(s.process.WaitError())
			return
		}
	}
}

// runDetection lets a detector scan the pane after output. It does not hold
// the session lock while the detector runs, since applying a detected state
// takes the agent's lock.
func (s *terminalSession) runDetection() {
	s.mu.Lock()
	detect := s.detect
	if detect == nil || time.Since(s.lastDetect) < detectInterval {
		s.mu.Unlock()
		return
	}
	s.lastDetect = time.Now()
	text := s.text(maxReadLines)
	s.mu.Unlock()
	detect(text)
}

// renderOutput feeds a chunk of PTY output to the screen and publishes the
// result, recovering from a panic in the terminal emulator so a bug in one
// pane's rendering crashes that pane instead of the whole daemon.
func (s *terminalSession) renderOutput(data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	err := recoverPanic(fmt.Sprintf("terminal %s render", s.metadata.ID), func() {
		_, _ = s.screen.Write(data)
		s.publish()
	})
	if err != nil {
		s.renderBroken = true
	}
	return err
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

// terminalSend injects input into a terminal that no client is driving. It
// refuses when a controller is attached: a terminal has at most one input
// source, so a script must not race a person typing. An agent that needs to
// type into a TUI-owned terminal should attach and claim control instead.
func (s *Server) terminalSend(raw json.RawMessage) (map[string]string, error) {
	var p struct {
		TerminalID string `json:"terminal_id"`
		Text       string `json:"text"`
		Enter      bool   `json:"enter"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("decode terminal send params: %w", err)
	}
	if p.Text == "" && !p.Enter {
		return nil, errors.New("terminal send needs text or enter")
	}
	s.mu.RLock()
	term := s.terminals[p.TerminalID]
	s.mu.RUnlock()
	if term == nil {
		return nil, fmt.Errorf("unknown terminal")
	}
	term.mu.Lock()
	defer term.mu.Unlock()
	if term.renderBroken {
		return nil, errors.New("terminal screen is no longer available")
	}
	if term.process == nil {
		return nil, errors.New("terminal is no longer running")
	}
	if term.controller != nil {
		return nil, errors.New("terminal has an attached controller; attach and claim it to send input")
	}
	text := p.Text
	if p.Enter {
		// Carriage return is what a terminal sends when Enter is pressed. A
		// raw-mode reader treats a line feed as an ordinary character.
		text += "\r"
	}
	term.screen.Input([]byte(text))
	return map[string]string{"status": "sent"}, nil
}

// waitForTerminal blocks until a terminal's output contains text, so an
// orchestrator can wait for a command to reach a point instead of polling
// terminal.read. It attaches as a viewer, never as the input controller, and
// fails rather than sitting once the terminal has stopped without a match.
func (s *Server) waitForTerminal(ctx context.Context, raw json.RawMessage) (map[string]any, error) {
	var p struct {
		TerminalID string `json:"terminal_id"`
		Contains   string `json:"contains"`
		Timeout    int    `json:"timeout_seconds"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("decode terminal wait params: %w", err)
	}
	if p.Contains == "" {
		return nil, errors.New("terminal wait needs text to look for")
	}
	timeout, err := waitTimeout(p.Timeout)
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	session := s.terminals[p.TerminalID]
	s.mu.RUnlock()
	if session == nil {
		return nil, fmt.Errorf("unknown terminal")
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	go guard("terminal.wait-cancel", func() {
		select {
		case <-s.stop:
			cancel()
		case <-ctx.Done():
		}
	})

	_, sub, detach, err := session.subscribe(false, true)
	if err != nil {
		return nil, err
	}
	defer detach()

	snapshot := func() map[string]any {
		session.mu.Lock()
		defer session.mu.Unlock()
		return map[string]any{
			"terminal_id": p.TerminalID,
			"state":       session.metadata.State,
			"text":        session.text(maxReadLines),
		}
	}
	settled := func() (bool, bool) {
		session.mu.Lock()
		defer session.mu.Unlock()
		return strings.Contains(session.text(maxReadLines), p.Contains), finishedState(session.metadata.State)
	}

	for {
		if matched, stopped := settled(); matched {
			return snapshot(), nil
		} else if stopped {
			return snapshot(), fmt.Errorf("terminal %q stopped before its output contained %q", p.TerminalID, p.Contains)
		}
		select {
		case event, open := <-sub.events:
			if !open {
				return snapshot(), fmt.Errorf("terminal %q is no longer being watched", p.TerminalID)
			}
			if event.Name == "terminal.exit" {
				return snapshot(), fmt.Errorf("terminal %q stopped before its output contained %q", p.TerminalID, p.Contains)
			}
		case <-ctx.Done():
			return snapshot(), fmt.Errorf("waiting for terminal %q output: %w", p.TerminalID, ctx.Err())
		case <-s.stop:
			return snapshot(), errors.New("daemon is shutting down")
		}
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
	go guard("terminal.attach-reader", func() {
		defer close(readerDone)
		for scanner.Scan() {
			b := append(json.RawMessage(nil), scanner.Bytes()...)
			select {
			case commands <- b:
			case <-s.stop:
				return
			}
		}
	})
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
