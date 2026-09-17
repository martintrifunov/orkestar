package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/martintrifunov/orkestar/internal/ipc"
)

func startPaneHistoryDaemon(t *testing.T, socket string) (*ipc.Client, func()) {
	t.Helper()
	server := NewServer(socket)
	server.SetPaneHistory(true)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx) }()
	client := ipc.NewClient(socket)
	deadline := time.Now().Add(3 * time.Second)
	for {
		pingContext, pingCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		err := client.Call(pingContext, "system.ping", nil, new(map[string]string))
		pingCancel()
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("daemon did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	return client, func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("server shutdown: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("server did not stop")
		}
	}
}

// Opt-in pane history is terminal text that survives a daemon restart. A
// stopped terminal's scrollback is readable again from the next daemon.
func TestPaneHistorySurvivesRestart(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "orkestar-pane-history-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	socket := filepath.Join(dir, "socket")

	client, stop := startPaneHistoryDaemon(t, socket)
	var w Workspace
	callRecovery(t, client, "workspace.create", map[string]string{"directory": dir}, &w)
	var started Terminal
	callRecovery(t, client, "terminal.start", map[string]any{
		"workspace_id": w.ID,
		"command":      []string{"/bin/sh", "-c", "printf 'persisted-line\\n'; sleep 5"},
	}, &started)

	var read struct {
		Text string `json:"text"`
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		callRecovery(t, client, "terminal.read", map[string]string{"terminal_id": started.ID}, &read)
		if strings.Contains(read.Text, "persisted-line") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("output never appeared before the restart: %q", read.Text)
		}
		time.Sleep(5 * time.Millisecond)
	}
	var stopped Terminal
	callRecovery(t, client, "terminal.stop", map[string]string{"terminal_id": started.ID}, &stopped)
	stop()

	restoredClient, stopRestored := startPaneHistoryDaemon(t, socket)
	defer stopRestored()
	var reread struct {
		Text  string `json:"text"`
		Lines int    `json:"lines"`
	}
	callRecovery(t, restoredClient, "terminal.read", map[string]string{"terminal_id": started.ID}, &reread)
	if !strings.Contains(reread.Text, "persisted-line") {
		t.Fatalf("the restored terminal lost its history: %q", reread.Text)
	}
}

func TestPaneHistoryIsOffByDefault(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "orkestar-pane-history-off-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	server := NewServer(filepath.Join(dir, "socket"))
	// This is synchronous and must not create a file while disabled.
	server.savePaneHistory()
	if _, err := os.Stat(filepath.Join(dir, paneHistoryFile)); !os.IsNotExist(err) {
		t.Fatalf("pane history was written while disabled: %v", err)
	}

	// A file left behind by an earlier opt-in is removed when the feature is
	// off, so disabling it does not leave terminal output on disk.
	path := filepath.Join(dir, paneHistoryFile)
	if err := os.WriteFile(path, []byte(`{"term_1":["secret"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	server.loadPaneHistory()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("pane history should be removed when disabled: %v", err)
	}
}
