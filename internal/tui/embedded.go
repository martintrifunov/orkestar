package tui

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/martintrifunov/orkestar/internal/ipc"
	"github.com/martintrifunov/orkestar/internal/terminal"
)

// embeddedTerminal owns one client attachment, never the underlying process.
// Output notifications are coalesced; a hidden or slow UI cannot block the PTY.
type embeddedTerminal struct {
	title, root   string
	editor        *textEditor
	review        *reviewPane
	terminalID    string
	stream        *ipc.Stream
	emulator      paneScreen
	view          *remoteScreen
	events        chan tea.Msg
	done          chan struct{}
	readDone      chan struct{}
	writeDone     chan struct{}
	closeOnce     sync.Once
	detachPending bool
	exited        bool
}

func (t *embeddedTerminal) close() {
	t.closeOnce.Do(func() {
		if t.done != nil {
			close(t.done)
		}
		if t.stream != nil {
			_ = t.stream.Close()
		}
		_ = t.emulator.Close()
	})
}

// embeddedReadyMsg reports the outcome of opening an embedded terminal.
type embeddedReadyMsg struct {
	terminal *embeddedTerminal
	err      error
}

// embeddedEventMsg is sent for every stream event on an embedded
// terminal's connection. exited is true once the underlying process has
// ended or the connection broke; Update should stop listening in that case
// rather than re-arming the read loop.
type embeddedEventMsg struct {
	terminalID string
	terminal   *embeddedTerminal
	exited     bool
	err        error
}

// openEmbeddedTerminal attaches to the daemon screen stream and requests
// initial dimensions when this client owns input.
func openEmbeddedTerminal(client *ipc.Client, terminalID string, columns, rows int) tea.Cmd {
	return openEmbeddedTerminalContext(context.Background(), client, terminalID, columns, rows)
}

func openEmbeddedTerminalContext(ctx context.Context, client *ipc.Client, terminalID string, columns, rows int) tea.Cmd {
	return func() tea.Msg {
		if columns <= 0 {
			columns = 80
		}
		if rows <= 0 {
			rows = 24
		}

		dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		var result struct {
			Screen     terminal.Frame `json:"screen"`
			Controller bool           `json:"controller"`
		}
		stream, err := client.OpenStream(dialCtx, "terminal.attach", map[string]any{"terminal_id": terminalID, "screen": true}, &result)
		if err != nil {
			return embeddedReadyMsg{err: err}
		}
		view := &remoteScreen{stream: stream, frame: result.Screen, controller: result.Controller}
		term := &embeddedTerminal{terminalID: terminalID, stream: stream, emulator: view, view: view, events: make(chan tea.Msg, 1), done: make(chan struct{}), readDone: make(chan struct{}), writeDone: make(chan struct{})}
		close(term.writeDone)
		go func() {
			select {
			case <-ctx.Done():
				term.close()
			case <-term.done:
			}
		}()
		if result.Controller {
			if err := stream.Send(resizeCommand(columns, rows)); err != nil {
				term.close()
				return embeddedReadyMsg{err: err}
			}
		}
		go embeddedReadLoop(term)
		return embeddedReadyMsg{terminal: term}
	}
}

// embeddedReadLoop applies complete daemon frames and control changes until
// the stream fails or the process exits.
func embeddedReadLoop(term *embeddedTerminal) {
	defer close(term.readDone)
	defer func() { term.close(); <-term.writeDone }()
	for {
		var event ipc.Event
		if err := term.stream.Receive(&event); err != nil {
			term.notify(true, err)
			return
		}
		switch event.Event {
		case "terminal.screen":
			var frame terminal.Frame
			if err := json.Unmarshal(event.Data, &frame); err != nil {
				term.notify(true, err)
				return
			}
			term.view.apply(frame)
			term.notify(false, nil)
		case "terminal.control":
			var status struct {
				Controller bool `json:"controller"`
			}
			_ = json.Unmarshal(event.Data, &status)
			term.view.mu.Lock()
			term.view.controller = status.Controller
			term.view.mu.Unlock()
			term.notify(false, nil)

		case "terminal.error":
			var result struct {
				Message string `json:"message"`
			}
			if json.Unmarshal(event.Data, &result) == nil {
				term.notify(false, errors.New(result.Message))
			}
		case "terminal.exit":
			term.notify(true, nil)
			return
		}
	}
}

func (term *embeddedTerminal) notify(exited bool, err error) {
	event := embeddedEventMsg{terminalID: term.terminalID, terminal: term, exited: exited, err: err}
	if exited {
		// Replace a pending repaint with the final event.
		select {
		case <-term.events:
		default:
		}
	}
	select {
	case term.events <- event:
	default:
	}
}

func waitEmbeddedEvent(term *embeddedTerminal) tea.Cmd {
	return func() tea.Msg {
		select {
		case event := <-term.events:
			return event
		case <-term.done:
			select {
			case event := <-term.events:
				return event
			default:
			}
			return embeddedEventMsg{terminalID: term.terminalID, terminal: term, exited: true}
		}
	}
}

// sendEmbeddedInput forwards user bytes to the daemon input owner.
func sendEmbeddedInput(stream *ipc.Stream, data []byte) error {
	err := stream.Send(map[string]any{
		"version": ipc.Version,
		"command": "input",
		"data":    base64.StdEncoding.EncodeToString(data),
	})
	debugf("sendEmbeddedInput: %d bytes err=%v", len(data), err)
	return err
}

// sendEmbeddedResize requests a daemon screen and PTY resize in input order.
func sendEmbeddedResize(term *embeddedTerminal, columns, rows int) {
	if term.stream == nil {
		term.emulator.Resize(columns, rows)
		return
	}
	if term.exited {
		return
	}
	if term.view != nil && !term.view.controls() {
		return
	}
	term.emulator.Resize(columns, rows)
	if err := term.stream.Send(resizeCommand(columns, rows)); err != nil {
		term.notify(true, err)
		term.close()
	}
}

func resizeCommand(columns, rows int) map[string]any {
	return map[string]any{
		"version": ipc.Version,
		"command": "resize",
		"columns": columns,
		"rows":    rows,
	}
}

// embeddedPaneSize returns the columns/rows available for the embedded
// terminal pane given the current window size, accounting for the sidebar
// and the pane's own border/padding.
func embeddedPaneSize(width, height int) (columns, rows int) {
	sidebarWidth := embeddedSidebarWidth(width)
	columns = width - sidebarWidth - 1 - 4 // gap + pane border/padding
	rows = height - 5                      // header + status/help lines + pane border
	if columns < 20 {
		columns = 20
	}
	if rows < 5 {
		rows = 5
	}
	return columns, rows
}

func embeddedSidebarWidth(width int) int {
	sidebarWidth := width / 3
	if sidebarWidth > 40 {
		sidebarWidth = 40
	}
	if sidebarWidth < 24 {
		sidebarWidth = 24
	}
	return sidebarWidth
}
