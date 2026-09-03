package daemon_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/martintrifunov/orkestar/internal/daemon"
	"github.com/martintrifunov/orkestar/internal/ipc"
)

func TestServerPingAndWorkspaceLifecycle(t *testing.T) {
	t.Parallel()

	temporaryDirectory := t.TempDir()
	socketDirectory, err := os.MkdirTemp("/tmp", "orkestar-test-")
	if err != nil {
		t.Fatalf("create socket directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDirectory) })
	socketPath := filepath.Join(socketDirectory, "orkestar.sock")
	server := daemon.NewServer(socketPath)
	ctx, cancel := context.WithCancel(context.Background())
	serverError := make(chan error, 1)
	go func() {
		serverError <- server.Serve(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		if err := <-serverError; err != nil {
			t.Errorf("server shutdown: %v", err)
		}
	})

	client := ipc.NewClient(socketPath)
	waitForServer(t, client)

	var ping struct {
		Status string `json:"status"`
	}
	callContext, callCancel := context.WithTimeout(context.Background(), time.Second)
	defer callCancel()
	if err := client.Call(callContext, "system.ping", nil, &ping); err != nil {
		t.Fatalf("ping daemon: %v", err)
	}
	if ping.Status != "ok" {
		t.Fatalf("unexpected ping status %q", ping.Status)
	}

	var workspace daemon.Workspace
	if err := client.Call(callContext, "workspace.create", map[string]string{
		"directory": temporaryDirectory,
		"name":      "test",
	}, &workspace); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	if workspace.Name != "test" || workspace.Directory != temporaryDirectory {
		t.Fatalf("unexpected workspace: %#v", workspace)
	}

	var snapshot daemon.Snapshot
	if err := client.Call(callContext, "system.snapshot", nil, &snapshot); err != nil {
		t.Fatalf("get snapshot: %v", err)
	}
	if len(snapshot.Workspaces) != 1 || snapshot.Workspaces[0].ID != workspace.ID {
		t.Fatalf("unexpected snapshot: %#v", snapshot)
	}
}

func TestTerminalSurvivesDetachAndReattach(t *testing.T) {
	t.Parallel()

	temporaryDirectory := t.TempDir()
	socketDirectory, err := os.MkdirTemp("/tmp", "orkestar-pty-test-")
	if err != nil {
		t.Fatalf("create socket directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDirectory) })
	socketPath := filepath.Join(socketDirectory, "orkestar.sock")
	server := daemon.NewServer(socketPath)
	ctx, cancel := context.WithCancel(context.Background())
	serverError := make(chan error, 1)
	go func() { serverError <- server.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-serverError; err != nil {
			t.Errorf("server shutdown: %v", err)
		}
	})

	client := ipc.NewClient(socketPath)
	waitForServer(t, client)
	callContext, callCancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer callCancel()

	var workspace daemon.Workspace
	if err := client.Call(callContext, "workspace.create", map[string]string{
		"directory": temporaryDirectory,
	}, &workspace); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	var started daemon.Terminal
	if err := client.Call(callContext, "terminal.start", map[string]any{
		"workspace_id": workspace.ID,
		"command": []string{
			"/bin/sh",
			"-c",
			"printf 'ready\\n'; IFS= read -r line; printf 'got:%s\\n' \"$line\"",
		},
		"columns": 80,
		"rows":    24,
	}, &started); err != nil {
		t.Fatalf("start terminal: %v", err)
	}

	firstStream, firstReplay := openTerminal(t, callContext, client, started.ID)
	readUntil(t, firstStream, firstReplay, []byte("ready"))
	if err := firstStream.Close(); err != nil {
		t.Fatalf("detach first client: %v", err)
	}

	secondStream, secondReplay := openTerminal(t, callContext, client, started.ID)
	defer secondStream.Close()
	if !bytes.Contains(secondReplay, []byte("ready")) {
		t.Fatalf("replay did not preserve prior output: %q", secondReplay)
	}
	if err := secondStream.Send(map[string]any{
		"version": ipc.Version,
		"command": "input",
		"data":    base64.StdEncoding.EncodeToString([]byte("hello\n")),
	}); err != nil {
		t.Fatalf("send terminal input: %v", err)
	}
	readUntil(t, secondStream, nil, []byte("got:hello"))
}

func openTerminal(t *testing.T, ctx context.Context, client *ipc.Client, terminalID string) (*ipc.Stream, []byte) {
	t.Helper()

	var result struct {
		Replay string `json:"replay"`
	}
	stream, err := client.OpenStream(ctx, "terminal.attach", map[string]string{
		"terminal_id": terminalID,
	}, &result)
	if err != nil {
		t.Fatalf("attach terminal: %v", err)
	}
	replay, err := base64.StdEncoding.DecodeString(result.Replay)
	if err != nil {
		stream.Close()
		t.Fatalf("decode terminal replay: %v", err)
	}
	return stream, replay
}

func readUntil(t *testing.T, stream *ipc.Stream, initial, expected []byte) {
	t.Helper()

	type result struct {
		output []byte
		err    error
	}
	resultChannel := make(chan result, 1)
	go func() {
		output := append([]byte(nil), initial...)
		for !bytes.Contains(output, expected) {
			var event ipc.Event
			if err := stream.Receive(&event); err != nil {
				resultChannel <- result{output: output, err: err}
				return
			}
			if event.Event != "terminal.output" {
				continue
			}
			var payload struct {
				Data string `json:"data"`
			}
			if err := json.Unmarshal(event.Data, &payload); err != nil {
				resultChannel <- result{output: output, err: err}
				return
			}
			data, err := base64.StdEncoding.DecodeString(payload.Data)
			if err != nil {
				resultChannel <- result{output: output, err: err}
				return
			}
			output = append(output, data...)
		}
		resultChannel <- result{output: output}
	}()

	select {
	case result := <-resultChannel:
		if result.err != nil {
			t.Fatalf("read terminal output %q: %v", result.output, result.err)
		}
	case <-time.After(3 * time.Second):
		stream.Close()
		t.Fatalf("timed out waiting for terminal output %q", expected)
	}
}

func waitForServer(t *testing.T, client *ipc.Client) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		var result map[string]string
		err := client.Call(ctx, "system.ping", nil, &result)
		cancel()
		if err == nil {
			return
		}
		if !ipc.IsUnavailable(err) && !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("wait for server: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("server did not become ready")
}
