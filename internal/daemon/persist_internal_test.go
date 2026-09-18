package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/martintrifunov/orkestar/internal/ipc"
)

type failingStore struct{ fail bool }

func (f *failingStore) Load(context.Context) ([]byte, error) { return nil, nil }
func (f *failingStore) Save(context.Context, []byte) error {
	if f.fail {
		return errors.New("disk full")
	}
	return nil
}
func (f *failingStore) Close() error { return nil }

func pingStatus(t *testing.T, s *Server) map[string]string {
	t.Helper()
	response, _ := s.handleRequest(context.Background(), ipc.Request{Version: ipc.Version, Method: "system.ping"})
	if response.Error != nil {
		t.Fatalf("ping: %+v", response.Error)
	}
	var status map[string]string
	if err := json.Unmarshal(response.Result, &status); err != nil {
		t.Fatalf("decode ping: %v", err)
	}
	return status
}

// A mutation is applied in memory before it is saved. When the save fails the
// daemon used to look healthy and forget the change on restart; now the
// failure is recorded, reported by status, and retried until it sticks.
func TestPersistFailureIsVisibleAndRetried(t *testing.T) {
	s := NewServer(filepath.Join(t.TempDir(), "orkestar.sock"))
	flaky := &failingStore{fail: true}
	s.store = flaky

	if err := s.persist(); err == nil {
		t.Fatal("a failed save was reported as success")
	}
	if s.lastPersistError() == "" {
		t.Fatal("the failure was not recorded")
	}
	if status := pingStatus(t, s); status["persist_error"] == "" {
		t.Fatalf("ping does not report the failed save: %+v", status)
	}

	// Once the disk accepts writes again, the retry clears the condition and
	// ping is healthy without a restart.
	flaky.fail = false
	s.retryPersistIfNeeded()
	if s.lastPersistError() != "" {
		t.Fatalf("the retry did not clear the failure: %s", s.lastPersistError())
	}
	if status := pingStatus(t, s); status["persist_error"] != "" {
		t.Fatalf("ping still reports a stale failure: %+v", status)
	}

	// While metadata is durable the retry must not rewrite the snapshot every
	// tick; it only exists to close a gap.
	counts := &countingStore{}
	s.store = counts
	s.retryPersistIfNeeded()
	if counts.saves != 0 {
		t.Fatalf("the retry wrote while metadata was already durable (%d saves)", counts.saves)
	}
}
