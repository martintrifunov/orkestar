package daemon

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/martintrifunov/orkestar/internal/ipc"
	"github.com/martintrifunov/orkestar/internal/terminal"
)

func TestDaemonAnswersQueriesWithoutClientAndArbitratesViewers(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "orkestar-screen-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	_, client, _ := serveRecoveryTest(t, filepath.Join(dir, "socket"))
	var w Workspace
	callRecovery(t, client, "workspace.create", map[string]string{"directory": dir}, &w)
	var started Terminal
	script := `stty -echo -icanon; printf '\033[H\033[6n'; dd bs=1 count=6 of=reply 2>/dev/null; printf '\033[?1049hready\r\n'; while IFS= read -r line; do printf 'received:%s\r\n' "$line"; done`
	callRecovery(t, client, "terminal.start", map[string]any{"workspace_id": w.ID, "command": []string{"/bin/sh", "-c", script}}, &started)
	deadline := time.Now().Add(2 * time.Second)
	for {
		b, _ := os.ReadFile(filepath.Join(dir, "reply"))
		if string(b) == "\x1b[1;1R" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("query was not answered while detached: %q", b)
		}
		time.Sleep(time.Millisecond)
	}
	open := func() (*ipc.Stream, bool) {
		var result struct {
			Screen     terminal.Frame `json:"screen"`
			Controller bool           `json:"controller"`
		}
		ctx, c := context.WithTimeout(context.Background(), time.Second)
		defer c()
		stream, err := client.OpenStream(ctx, "terminal.attach", map[string]any{"terminal_id": started.ID, "screen": true}, &result)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = stream.Close() })
		return stream, result.Controller
	}
	owner, control := open()
	if !control {
		t.Fatal("first client did not receive control")
	}
	viewer, control := open()
	if control {
		t.Fatal("second client received control")
	}
	send := func(stream *ipc.Stream, command string, data any) {
		t.Helper()
		if err := stream.Send(map[string]any{"version": ipc.Version, "command": command, "data": data}); err != nil {
			t.Fatal(err)
		}
	}
	send(viewer, "input", base64.StdEncoding.EncodeToString([]byte("forbidden\n")))
	send(owner, "input", base64.StdEncoding.EncodeToString([]byte("first\n")))
	waitFrame := func(stream *ipc.Stream, want string) terminal.Frame {
		result := make(chan terminal.Frame, 1)
		go func() {
			for {
				var e ipc.Event
				if stream.Receive(&e) != nil {
					return
				}
				if e.Event == "terminal.screen" {
					var f terminal.Frame
					_ = json.Unmarshal(e.Data, &f)
					if strings.Contains(f.Content, want) {
						result <- f
						return
					}
				}
			}
		}()
		select {
		case f := <-result:
			return f
		case <-time.After(2 * time.Second):
			_ = stream.Close()
			t.Fatalf("no frame containing %q", want)
		}
		return terminal.Frame{}
	}
	frame := waitFrame(viewer, "received:first")
	if strings.Contains(frame.Content, "forbidden") {
		t.Fatal("viewer input reached the PTY")
	}
	_ = owner.Close()
	// Reconnect after the old controller's socket has been released.
	claimDeadline := time.Now().Add(time.Second)
	for {
		send(viewer, "claim", nil)
		var e ipc.Event
		if err := viewer.Receive(&e); err != nil {
			t.Fatal(err)
		}
		if e.Event == "terminal.control" {
			var c struct{ Controller bool }
			_ = json.Unmarshal(e.Data, &c)
			if c.Controller {
				break
			}
		}
		if time.Now().After(claimDeadline) {
			t.Fatal("viewer could not claim released control")
		}
	}
	send(viewer, "input", base64.StdEncoding.EncodeToString([]byte("second\n")))
	frame = waitFrame(viewer, "received:second")
	if strings.Contains(frame.Content, "1;1R") {
		t.Fatal("reconnection replayed a terminal-query reply as input")
	}
	var history struct {
		Screen  terminal.Frame
		History []string
	}
	callRecovery(t, client, "terminal.history", map[string]string{"terminal_id": started.ID}, &history)
	if !strings.Contains(history.Screen.Content, "received:second") {
		t.Fatal("authoritative screen lost latest output")
	}
}
