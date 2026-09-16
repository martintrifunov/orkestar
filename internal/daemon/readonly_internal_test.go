package daemon

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/martintrifunov/orkestar/internal/ipc"
)

type countingStore struct{ saves int }

func (c *countingStore) Load(context.Context) ([]byte, error) { return nil, nil }
func (c *countingStore) Save(context.Context, []byte) error   { c.saves++; return nil }
func (c *countingStore) Close() error                         { return nil }

// TestReadOnlyMethodsDoNotPersist pins permission.list as a no-write method: a
// client polling it must not rewrite the whole snapshot to SQLite on every
// call, or a read contends with lifecycle persistence for the same mutex.
func TestReadOnlyMethodsDoNotPersist(t *testing.T) {
	s := NewServer(filepath.Join(t.TempDir(), "orkestar.sock"))
	counts := &countingStore{}
	s.store = counts

	if _, _ = s.handleRequest(ipc.Request{Version: ipc.Version, Method: "permission.list"}); counts.saves != 0 {
		t.Fatalf("permission.list wrote the snapshot %d times", counts.saves)
	}

	params, err := json.Marshal(map[string]string{"directory": t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if _, _ = s.handleRequest(ipc.Request{Version: ipc.Version, Method: "workspace.create", Params: params}); counts.saves != 1 {
		t.Fatalf("workspace.create did not persist (saves=%d)", counts.saves)
	}
}
