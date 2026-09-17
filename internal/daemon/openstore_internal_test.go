package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/martintrifunov/orkestar/internal/store"
)

// A metadata row the daemon cannot read must not brick it: the daemon starts
// empty and keeps the unreadable database aside, so it can still be reset.
func TestUnreadableMetadataStartsEmptyAndKeepsTheDatabase(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "orkestar-corrupt-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "metadata.db")

	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Save(context.Background(), []byte("not json")); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	server := NewServer(filepath.Join(dir, "socket"))
	if err := server.openStore(); err != nil {
		t.Fatalf("a corrupt database should not stop the daemon: %v", err)
	}
	defer func() {
		if server.store != nil {
			_ = server.store.Close()
		}
	}()
	if _, err := os.Stat(path + ".corrupt"); err != nil {
		t.Fatalf("the unreadable database was not kept aside: %v", err)
	}
}

// Disabling pane history must clear the file even on a start with no metadata
// to restore, so terminal output does not linger.
func TestOpenStoreRemovesPaneHistoryWhenDisabled(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "orkestar-ph-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	history := filepath.Join(dir, paneHistoryFile)
	if err := os.WriteFile(history, []byte(`{"term_1":["secret"]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	server := NewServer(filepath.Join(dir, "socket"))
	if err := server.openStore(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if server.store != nil {
			_ = server.store.Close()
		}
	}()
	if _, err := os.Stat(history); !os.IsNotExist(err) {
		t.Fatalf("pane history was not removed when disabled: %v", err)
	}
}

// A stale attention reason saved before a restart must not survive on an
// interrupted agent that cannot be resumed.
func TestRestoreClearsStaleAttentionReason(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "orkestar-attention-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "metadata.db")

	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(Snapshot{Agents: []Agent{{
		ID: "a1", Adapter: "manifest-agent", State: "waiting_input", AttentionReason: "detected in the pane",
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Save(context.Background(), encoded); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	server := NewServer(filepath.Join(dir, "socket"))
	if err := server.openStore(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if server.store != nil {
			_ = server.store.Close()
		}
	}()
	entry := server.agents["a1"]
	if entry == nil {
		t.Fatal("the agent was not restored")
	}
	if reason := entry.snapshot().AttentionReason; reason != "" {
		t.Fatalf("a stale attention reason survived the restart: %q", reason)
	}
}
