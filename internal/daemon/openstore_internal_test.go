package daemon

import (
	"context"
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
