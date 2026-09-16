//go:build !windows

package ipc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
	"time"
)

func Dial(ctx context.Context, path string, timeout time.Duration) (net.Conn, error) {
	return (&net.Dialer{Timeout: timeout}).DialContext(ctx, "unix", path)
}
func Listen(path string) (net.Listener, error) { return net.Listen("unix", path) }
func RestrictListener(path string) error       { return os.Chmod(path, 0600) }
func RemoveListener(path string) error         { return os.Remove(path) }

// socketProbeTimeout bounds how long startup waits to learn whether a socket
// path is live before deciding the leftover is stale.
const socketProbeTimeout = 150 * time.Millisecond

func PrepareListener(path string) error {
	return prepareListener(path, func(path string, timeout time.Duration) (net.Conn, error) {
		return net.DialTimeout("unix", path, timeout)
	})
}

func prepareListener(path string, dial func(string, time.Duration) (net.Conn, error)) error {
	conn, err := dial(path, socketProbeTimeout)
	if err == nil {
		conn.Close()
		return ErrAlreadyRunning
	}
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	// A refused connection, or something at the path that is not a socket,
	// proves nothing is listening, so the leftover can be removed. Anything
	// else — a dial timeout, a full accept backlog, a permission error — may
	// be a live daemon that is merely busy. Never unlink its socket in that
	// case: a second daemon would bind the same path and the first would be
	// unreachable while it still owns every session.
	if !errors.Is(err, syscall.ECONNREFUSED) && !errors.Is(err, syscall.ENOTSOCK) {
		return ErrAlreadyRunning
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove stale daemon socket: %w", err)
	}
	return nil
}
