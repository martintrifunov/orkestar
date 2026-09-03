package tui

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/vt"

	"github.com/martintrifunov/orkestar/internal/ipc"
)

// embeddedTerminal is a terminal rendered inline as a pane in the
// dashboard, instead of handing the real TTY to a subprocess. It attaches
// to the daemon's terminal.attach stream directly and feeds the raw PTY
// byte stream into a virtual terminal emulator, whose rendered screen
// becomes the pane's content.
//
// emulator is a *vt.SafeEmulator, but that safety is incomplete as of the
// pinned (unreleased, pre-v0) version: it wraps Write, Render, and Resize
// with a mutex, but not every *vt.Emulator method it embeds — String, for
// one, is unprotected and races against the reader goroutine's Write
// calls. Only call Write/Render/Resize on it from outside the reader
// goroutine; do not add calls to other Emulator methods without checking
// safe_emulator.go first.
type embeddedTerminal struct {
	terminalID    string
	stream        *ipc.Stream
	emulator      *vt.SafeEmulator
	events        chan tea.Msg
	detachPending bool
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
	exited     bool
	err        error
}

// openEmbeddedTerminal attaches to terminalID's stream, seeds a new
// terminal emulator sized columns x rows with the replay, and starts a
// background goroutine that keeps the emulator (and the daemon's PTY, via
// an initial resize) in sync with it.
func openEmbeddedTerminal(client *ipc.Client, terminalID string, columns, rows int) tea.Cmd {
	return func() tea.Msg {
		if columns <= 0 {
			columns = 80
		}
		if rows <= 0 {
			rows = 24
		}

		dialCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
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

		emulator := vt.NewSafeEmulator(columns, rows)
		_, _ = emulator.Write(replay)

		// The terminal may have been created (or last resized) for
		// different dimensions than this pane's; bring the daemon's PTY
		// in line with what we're actually about to render.
		if err := stream.Send(resizeCommand(columns, rows)); err != nil {
			stream.Close()
			return embeddedReadyMsg{err: err}
		}

		term := &embeddedTerminal{
			terminalID: terminalID,
			stream:     stream,
			emulator:   emulator,
			events:     make(chan tea.Msg, 64),
		}
		debugf("openEmbeddedTerminal: attached terminalID=%s columns=%d rows=%d", terminalID, columns, rows)
		go embeddedReadLoop(term)
		return embeddedReadyMsg{terminal: term}
	}
}

// embeddedReadLoop applies incoming terminal.output events to the
// emulator and reports every event (or the terminal's end) on
// term.events, until the stream errors or the process exits.
func embeddedReadLoop(term *embeddedTerminal) {
	for {
		var event ipc.Event
		if err := term.stream.Receive(&event); err != nil {
			term.events <- embeddedEventMsg{terminalID: term.terminalID, exited: true, err: err}
			return
		}

		switch event.Event {
		case "terminal.output":
			var payload struct {
				Data string `json:"data"`
			}
			if json.Unmarshal(event.Data, &payload) != nil {
				continue
			}
			data, err := base64.StdEncoding.DecodeString(payload.Data)
			if err != nil {
				continue
			}
			_, _ = term.emulator.Write(data)
			term.events <- embeddedEventMsg{terminalID: term.terminalID}
		case "terminal.exit":
			term.events <- embeddedEventMsg{terminalID: term.terminalID, exited: true}
			return
		}
	}
}

// waitEmbeddedEvent blocks for the next event from an embedded terminal.
// Update re-issues this after handling each event, forming a read loop
// (the same pattern as tick()).
func waitEmbeddedEvent(events chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		return <-events
	}
}

// sendEmbeddedInput forwards raw bytes to the daemon as terminal input.
//
// This is called synchronously from Update, not wrapped in a tea.Cmd:
// Bubble Tea gives no ordering guarantee across concurrently-scheduled
// Cmd goroutines, so a Cmd per keystroke could let fast typing reach the
// daemon out of order. Update already runs strictly one message at a
// time, and this is a local unix-socket write (microseconds), so calling
// it inline preserves keystroke order at a cost too small to matter.
func sendEmbeddedInput(stream *ipc.Stream, data []byte) {
	err := stream.Send(map[string]any{
		"version": ipc.Version,
		"command": "input",
		"data":    base64.StdEncoding.EncodeToString(data),
	})
	debugf("sendEmbeddedInput: %d bytes %q err=%v", len(data), data, err)
}

// sendEmbeddedResize resizes the local emulator and tells the daemon to
// resize the underlying PTY to match. Also called synchronously, for the
// same reason as sendEmbeddedInput: a resize racing a keystroke's Cmd
// could reach the daemon in the wrong order.
func sendEmbeddedResize(term *embeddedTerminal, columns, rows int) {
	term.emulator.Resize(columns, rows)
	_ = term.stream.Send(resizeCommand(columns, rows))
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
