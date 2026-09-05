package daemon

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/martintrifunov/orkestar/internal/ipc"
)

func TestWindowsPowerShellReconnectAndReset(t *testing.T) {
	path := filepath.Join(t.TempDir(), "orkestar.sock")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	server := NewServer(path)
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(10 * time.Second):
			t.Error("shutdown timed out")
		}
	}()
	client := ipc.NewClient(path)
	for {
		if client.Call(ctx, "system.ping", nil, nil) == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(25 * time.Millisecond):
		}
	}
	var workspace Workspace
	call := func(method string, params, result any) {
		t.Helper()
		if err := client.Call(ctx, method, params, result); err != nil {
			t.Fatal(err)
		}
	}
	call("workspace.create", map[string]string{"directory": t.TempDir()}, &workspace)
	var terminal Terminal
	call("terminal.start", map[string]any{"workspace_id": workspace.ID, "command": []string{"powershell.exe", "-NoLogo", "-NoProfile", "-Command", "Write-Output 'windows-ready'; Start-Sleep -Seconds 60"}}, &terminal)
	ready := false
	for !ready {
		var result struct {
			Replay string `json:"replay"`
			Frame  struct {
				Content string `json:"content"`
			} `json:"screen"`
		}
		stream, err := client.OpenStream(ctx, "terminal.attach", map[string]any{"terminal_id": terminal.ID, "screen": true}, &result)
		if err != nil {
			t.Fatal(err)
		}
		stream.Close()
		ready = strings.Contains(result.Frame.Content, "windows-ready")
		if !ready {
			select {
			case <-ctx.Done():
				t.Fatal("PowerShell output missing")
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
	var state Snapshot
	call("system.snapshot", nil, &state)
	if len(state.Terminals) != 1 || state.Terminals[0].ID != terminal.ID || state.Terminals[0].State != "running" {
		t.Fatalf("disconnect stopped session: %+v", state.Terminals)
	}
	call("system.reset", map[string]bool{"confirm": true}, nil)
	call("system.snapshot", nil, &state)
	if len(state.Terminals)+len(state.Workspaces) != 0 {
		t.Fatal("reset retained state")
	}
}
