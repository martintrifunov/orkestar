package daemonclient

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Stopping when nothing runs is not an error: reset and daemon stop must be
// idempotent, or a stopped daemon cannot be reset.
func TestStopIsIdempotentWhenNoDaemonRuns(t *testing.T) {
	// A short path: Unix sockets have a length limit, and an over-long one
	// fails with EINVAL rather than "not found".
	dir, err := os.MkdirTemp("/tmp", "orkestarc-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := Stop(ctx, filepath.Join(dir, "s.sock")); err != nil {
		t.Fatalf("stopping with no daemon should succeed, got %v", err)
	}
}
