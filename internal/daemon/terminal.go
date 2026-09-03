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

const scrollbackCapacity = 2 * 1024 * 1024

type Terminal struct {
	ID          string    `json:"id"`
	WorkspaceID string    `json:"workspace_id"`
	Command     []string  `json:"command"`
	Directory   string    `json:"directory"`
	State       string    `json:"state"`
	ExitError   string    `json:"exit_error,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

type terminalEvent struct {
	Name string
	Data []byte
}

type terminalSession struct {
	process *pty.Process
	buffer  *terminal.Buffer

	mu          sync.Mutex
	metadata    Terminal
	subscribers map[chan terminalEvent]struct{}
	controller  bool
}

func newTerminalSession(metadata Terminal, process *pty.Process) *terminalSession {
	session := &terminalSession{
		process:     process,
		buffer:      terminal.NewBuffer(scrollbackCapacity),
		metadata:    metadata,
		subscribers: make(map[chan terminalEvent]struct{}),
	}
	go session.captureOutput()
	return session
}

func (s *terminalSession) snapshot() Terminal {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.metadata
}

func (s *terminalSession) attach() ([]byte, <-chan terminalEvent, func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.controller {
		return nil, nil, nil, errors.New("terminal already has an attached controller")
	}

	s.controller = true
	events := make(chan terminalEvent, 256)
	s.subscribers[events] = struct{}{}
	replay := s.buffer.Bytes()
	if s.metadata.State != "running" {
		if payload, err := json.Marshal(s.metadata); err == nil {
			events <- terminalEvent{Name: "terminal.exit", Data: payload}
		}
	}
	detach := func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if _, ok := s.subscribers[events]; ok {
			delete(s.subscribers, events)
			close(events)
		}
		s.controller = false
	}
	return replay, events, detach, nil
}

func (s *terminalSession) input(encoded string) error {
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return fmt.Errorf("decode terminal input: %w", err)
	}
	if _, err := s.process.Write(data); err != nil {
		return fmt.Errorf("write terminal input: %w", err)
	}
	return nil
}

func (s *terminalSession) resize(columns, rows int) error {
	return s.process.Resize(columns, rows)
}

func (s *terminalSession) close() error {
	return s.process.Close()
}

func (s *terminalSession) captureOutput() {
	data := make([]byte, 32*1024)
	for {
		count, err := s.process.Read(data)
		if count > 0 {
			chunk := append([]byte(nil), data[:count]...)
			s.mu.Lock()
			s.buffer.Write(chunk)
			for subscriber := range s.subscribers {
				select {
				case subscriber <- terminalEvent{Name: "terminal.output", Data: chunk}:
				default:
				}
			}
			s.mu.Unlock()
		}
		if err != nil {
			s.finish(s.process.WaitError())
			return
		}
	}
}

func (s *terminalSession) finish(waitError error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if waitError != nil {
		s.metadata.State = "crashed"
		s.metadata.ExitError = waitError.Error()
	} else {
		s.metadata.State = "stopped"
	}
	payload, _ := json.Marshal(s.metadata)
	for subscriber := range s.subscribers {
		select {
		case subscriber <- terminalEvent{Name: "terminal.exit", Data: payload}:
		default:
		}
	}
}

func (s *Server) startTerminal(rawParams json.RawMessage) (Terminal, error) {
	var params struct {
		WorkspaceID string   `json:"workspace_id"`
		Command     []string `json:"command"`
		Directory   string   `json:"directory"`
		Columns     int      `json:"columns"`
		Rows        int      `json:"rows"`
	}
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return Terminal{}, fmt.Errorf("decode terminal params: %w", err)
	}
	if len(params.Command) == 0 || params.Command[0] == "" {
		return Terminal{}, errors.New("terminal command is required")
	}

	s.mu.RLock()
	workspace, ok := s.workspaces[params.WorkspaceID]
	s.mu.RUnlock()
	if !ok {
		return Terminal{}, fmt.Errorf("workspace %q does not exist", params.WorkspaceID)
	}
	if params.Directory == "" {
		params.Directory = workspace.Directory
	}

	id, err := newID("term")
	if err != nil {
		return Terminal{}, err
	}
	process, err := pty.Start(pty.StartOptions{
		Command:   params.Command[0],
		Arguments: params.Command[1:],
		Directory: params.Directory,
		Columns:   params.Columns,
		Rows:      params.Rows,
	})
	if err != nil {
		return Terminal{}, err
	}

	metadata := Terminal{
		ID:          id,
		WorkspaceID: params.WorkspaceID,
		Command:     append([]string(nil), params.Command...),
		Directory:   params.Directory,
		State:       "running",
		CreatedAt:   time.Now().UTC(),
	}
	session := newTerminalSession(metadata, process)
	s.mu.Lock()
	s.terminals[id] = session
	s.mu.Unlock()
	return metadata, nil
}

func (s *Server) handleTerminalAttach(connection net.Conn, scanner *bufio.Scanner, encoder *json.Encoder, request ipc.Request) {
	if request.Version != ipc.Version {
		_ = encoder.Encode(ipc.NewErrorResponse(request.ID, "unsupported_version", "unsupported protocol version"))
		return
	}
	var params struct {
		TerminalID string `json:"terminal_id"`
	}
	if err := json.Unmarshal(request.Params, &params); err != nil {
		_ = encoder.Encode(ipc.NewErrorResponse(request.ID, "invalid_params", "invalid terminal attach params"))
		return
	}

	s.mu.RLock()
	session, ok := s.terminals[params.TerminalID]
	s.mu.RUnlock()
	if !ok {
		_ = encoder.Encode(ipc.NewErrorResponse(request.ID, "not_found", fmt.Sprintf("terminal %q does not exist", params.TerminalID)))
		return
	}

	replay, events, detach, err := session.attach()
	if err != nil {
		_ = encoder.Encode(ipc.NewErrorResponse(request.ID, "terminal_busy", err.Error()))
		return
	}
	defer detach()

	response, err := ipc.NewResponse(request.ID, map[string]any{
		"terminal": session.snapshot(),
		"replay":   base64.StdEncoding.EncodeToString(replay),
	})
	if err != nil || encoder.Encode(response) != nil {
		return
	}

	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for scanner.Scan() {
			var command struct {
				Version int    `json:"version"`
				Command string `json:"command"`
				Data    string `json:"data,omitempty"`
				Columns int    `json:"columns,omitempty"`
				Rows    int    `json:"rows,omitempty"`
			}
			if json.Unmarshal(scanner.Bytes(), &command) != nil || command.Version != ipc.Version {
				continue
			}
			switch command.Command {
			case "input":
				_ = session.input(command.Data)
			case "resize":
				_ = session.resize(command.Columns, command.Rows)
			case "detach":
				return
			}
		}
	}()

	for {
		select {
		case <-readerDone:
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			var payload any
			if event.Name == "terminal.output" {
				payload = map[string]string{
					"terminal_id": params.TerminalID,
					"data":        base64.StdEncoding.EncodeToString(event.Data),
				}
			} else {
				var terminal Terminal
				if json.Unmarshal(event.Data, &terminal) != nil {
					continue
				}
				payload = terminal
			}
			data, err := json.Marshal(payload)
			if err != nil {
				return
			}
			if err := encoder.Encode(ipc.Event{Version: ipc.Version, Event: event.Name, Data: data}); err != nil {
				return
			}
		}
	}
}
