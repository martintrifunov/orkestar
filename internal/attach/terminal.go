//go:build !windows

package attach

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/charmbracelet/x/term"

	"github.com/martintrifunov/orkestar/internal/ipc"
)

const detachPrefix = byte(0x02)

type attachResult struct {
	Replay string `json:"replay"`
}

type streamCommand struct {
	Version int    `json:"version"`
	Command string `json:"command"`
	Data    string `json:"data,omitempty"`
	Columns int    `json:"columns,omitempty"`
	Rows    int    `json:"rows,omitempty"`
}

func Terminal(ctx context.Context, client *ipc.Client, terminalID string, input *os.File, output io.Writer) error {
	if !term.IsTerminal(input.Fd()) {
		return errors.New("terminal attach requires an interactive TTY")
	}

	var result attachResult
	stream, err := client.OpenStream(ctx, "terminal.attach", map[string]string{
		"terminal_id": terminalID,
	}, &result)
	if err != nil {
		return err
	}
	defer stream.Close()

	previousState, err := term.MakeRaw(input.Fd())
	if err != nil {
		return fmt.Errorf("enable raw terminal mode: %w", err)
	}
	defer term.Restore(input.Fd(), previousState)

	_, _ = fmt.Fprint(output, "\x1b[?1049h\x1b[2J\x1b[H")
	defer fmt.Fprint(output, "\x1b[?1049l")

	replay, err := base64.StdEncoding.DecodeString(result.Replay)
	if err != nil {
		return fmt.Errorf("decode terminal replay: %w", err)
	}
	if _, err := output.Write(replay); err != nil {
		return fmt.Errorf("render terminal replay: %w", err)
	}
	if err := sendSize(stream, input); err != nil {
		return err
	}

	resizeSignals := make(chan os.Signal, 1)
	signal.Notify(resizeSignals, syscall.SIGWINCH)
	defer signal.Stop(resizeSignals)

	resultChannel := make(chan error, 2)
	go receiveOutput(stream, output, resultChannel)
	go forwardInput(stream, input, resultChannel)

	for {
		select {
		case <-ctx.Done():
			_ = stream.Send(streamCommand{Version: ipc.Version, Command: "detach"})
			return ctx.Err()
		case <-resizeSignals:
			if err := sendSize(stream, input); err != nil {
				return err
			}
		case err := <-resultChannel:
			return err
		}
	}
}

func sendSize(stream *ipc.Stream, input *os.File) error {
	columns, rows, err := term.GetSize(input.Fd())
	if err != nil {
		return fmt.Errorf("read terminal size: %w", err)
	}
	return stream.Send(streamCommand{
		Version: ipc.Version,
		Command: "resize",
		Columns: columns,
		Rows:    rows,
	})
}

func receiveOutput(stream *ipc.Stream, output io.Writer, result chan<- error) {
	for {
		var event ipc.Event
		if err := stream.Receive(&event); err != nil {
			result <- err
			return
		}
		if event.Version != ipc.Version {
			result <- fmt.Errorf("unsupported stream protocol version %d", event.Version)
			return
		}
		switch event.Event {
		case "terminal.output":
			var payload struct {
				Data string `json:"data"`
			}
			if err := json.Unmarshal(event.Data, &payload); err != nil {
				result <- fmt.Errorf("decode output event: %w", err)
				return
			}
			data, err := base64.StdEncoding.DecodeString(payload.Data)
			if err != nil {
				result <- fmt.Errorf("decode terminal output: %w", err)
				return
			}
			if _, err := output.Write(data); err != nil {
				result <- fmt.Errorf("render terminal output: %w", err)
				return
			}
		case "terminal.exit":
			result <- nil
			return
		}
	}
}

func forwardInput(stream *ipc.Stream, input io.Reader, result chan<- error) {
	buffer := make([]byte, 4096)
	prefixPending := false
	for {
		count, err := input.Read(buffer)
		if count > 0 {
			outgoing := make([]byte, 0, count+1)
			for _, character := range buffer[:count] {
				if prefixPending {
					prefixPending = false
					if character == 'q' {
						_ = stream.Send(streamCommand{Version: ipc.Version, Command: "detach"})
						result <- nil
						return
					}
					outgoing = append(outgoing, detachPrefix, character)
					continue
				}
				if character == detachPrefix {
					prefixPending = true
					continue
				}
				outgoing = append(outgoing, character)
			}
			if len(outgoing) > 0 {
				if sendErr := stream.Send(streamCommand{
					Version: ipc.Version,
					Command: "input",
					Data:    base64.StdEncoding.EncodeToString(outgoing),
				}); sendErr != nil {
					result <- sendErr
					return
				}
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				result <- nil
			} else {
				result <- fmt.Errorf("read terminal input: %w", err)
			}
			return
		}
	}
}
