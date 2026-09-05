package tui

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/martintrifunov/orkestar/internal/terminal"

	"github.com/martintrifunov/orkestar/internal/ipc"
)

// embeddedTerminal owns one client attachment, never the underlying process.
// Output notifications are coalesced; a hidden or slow UI cannot block the PTY.
type embeddedTerminal struct {
	terminalID    string
	stream        *ipc.Stream
	emulator      *terminal.Screen
	events        chan tea.Msg
	done          chan struct{}
	readDone      chan struct{}
	writeDone     chan struct{}
	closeOnce     sync.Once
	detachPending bool
}

func (t *embeddedTerminal) close() {
	t.closeOnce.Do(func() {
		close(t.done)
		_ = t.stream.Close()
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

// openEmbeddedTerminal attaches to terminalID's stream, seeds a new
// terminal emulator sized columns x rows with the replay, and starts a
// background goroutine that keeps the emulator (and the daemon's PTY, via
// an initial resize) in sync with it.
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
			Replay string `json:"replay"`
		}
		stream, err := client.OpenStream(dialCtx, "terminal.attach", map[string]string{
			"terminal_id": terminalID,
		}, &result)
		if err != nil {
			return embeddedReadyMsg{err: err}
		}

		replay, err := base64.StdEncoding.DecodeString(result.Replay)
		if err != nil {
			stream.Close()
			return embeddedReadyMsg{err: err}
		}

		term := &embeddedTerminal{
			terminalID: terminalID, stream: stream,
			emulator: terminal.NewScreen(columns, rows), events: make(chan tea.Msg, 1),
			done: make(chan struct{}), readDone: make(chan struct{}), writeDone: make(chan struct{}),
		}
		// Start draining replies before replay: replay can itself contain queries.
		go embeddedWriteLoop(term)
		go func() {
			select {
			case <-ctx.Done():
				term.close()
			case <-term.done:
			}
		}()
		_, _ = term.emulator.Write(replay)
		if err := stream.Send(resizeCommand(columns, rows)); err != nil {
			term.close()
			return embeddedReadyMsg{err: err}
		}
		go embeddedReadLoop(term)
		return embeddedReadyMsg{terminal: term}
	}
}

// embeddedReadLoop applies incoming terminal.output events to the
// emulator and reports every event (or the terminal's end) on
// term.events, until the stream errors or the process exits.
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
		case "terminal.output":
			var payload struct {
				Data string `json:"data"`
			}
			if err := json.Unmarshal(event.Data, &payload); err != nil {
				term.notify(true, err)
				return
			}
			data, err := base64.StdEncoding.DecodeString(payload.Data)
			if err != nil {
				term.notify(true, err)
				return
			}
			_, _ = term.emulator.Write(data)
			term.notify(false, nil)
		case "terminal.exit":
			term.notify(true, nil)
			return
		}
	}
}

// All user input and terminal replies share this writer, preserving order.
func embeddedWriteLoop(term *embeddedTerminal) {
	defer close(term.writeDone)
	buffer := make([]byte, 4096)
	for {
		n, err := term.emulator.Read(buffer)
		if n > 0 {
			if err := sendEmbeddedInput(term.stream, buffer[:n]); err != nil {
				term.notify(true, err)
				term.close()
				return
			}
		}
		if err != nil {
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

// sendEmbeddedInput forwards bytes from the single emulator-input pump.
func sendEmbeddedInput(stream *ipc.Stream, data []byte) error {
	err := stream.Send(map[string]any{
		"version": ipc.Version,
		"command": "input",
		"data":    base64.StdEncoding.EncodeToString(data),
	})
	debugf("sendEmbeddedInput: %d bytes err=%v", len(data), err)
	return err
}

// sendEmbeddedResize resizes the local emulator and tells the daemon to
// resize the underlying PTY to match. Also called synchronously, for the
// same reason as sendEmbeddedInput: a resize racing a keystroke's Cmd
// could reach the daemon in the wrong order.
func sendEmbeddedResize(term *embeddedTerminal, columns, rows int) {
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
