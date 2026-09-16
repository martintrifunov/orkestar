//go:build !windows

package ipc

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func socketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "orkestar-ipc-")
	if err != nil {
		t.Fatalf("create socket directory: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "orkestar.sock")
}

func TestPrepareListenerRemovesStaleSocket(t *testing.T) {
	t.Parallel()

	path := socketPath(t)
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	listener.(*net.UnixListener).SetUnlinkOnClose(false)
	if err := listener.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected stale socket file to remain: %v", err)
	}

	if err := PrepareListener(path); err != nil {
		t.Fatalf("prepare listener over a stale socket: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected stale socket to be removed, stat err: %v", err)
	}
}

func TestPrepareListenerKeepsLiveSocket(t *testing.T) {
	t.Parallel()

	path := socketPath(t)
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	if err := PrepareListener(path); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("expected ErrAlreadyRunning for a live socket, got %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected live socket to be preserved, stat err: %v", err)
	}
}

// A probe that times out means the daemon may be alive but busy. Removing its
// socket there would let a second daemon bind the same path while the first
// still owns every session.
func TestPrepareListenerKeepsSocketWhenProbeTimesOut(t *testing.T) {
	t.Parallel()

	path := socketPath(t)
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("create socket path: %v", err)
	}
	dial := func(string, time.Duration) (net.Conn, error) {
		return nil, &net.OpError{Op: "dial", Net: "unix", Err: os.ErrDeadlineExceeded}
	}
	if err := prepareListener(path, dial); !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("expected ErrAlreadyRunning on probe timeout, got %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected socket path to be preserved on timeout, stat err: %v", err)
	}
}

func TestPrepareListenerMissingSocket(t *testing.T) {
	t.Parallel()

	if err := PrepareListener(filepath.Join(socketPath(t), "nope.sock")); err != nil {
		t.Fatalf("prepare listener with no socket: %v", err)
	}
}
