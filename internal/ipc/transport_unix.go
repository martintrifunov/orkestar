//go:build !windows

package ipc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"time"
)

func Dial(ctx context.Context, path string, timeout time.Duration) (net.Conn, error) {
	return (&net.Dialer{Timeout: timeout}).DialContext(ctx, "unix", path)
}
func Listen(path string) (net.Listener, error) { return net.Listen("unix", path) }
func RestrictListener(path string) error       { return os.Chmod(path, 0600) }
func RemoveListener(path string) error         { return os.Remove(path) }
func PrepareListener(path string) error {
	conn, err := net.DialTimeout("unix", path, 150*time.Millisecond)
	if err == nil {
		conn.Close()
		return ErrAlreadyRunning
	}
	if !errors.Is(err, os.ErrNotExist) {
		var op *net.OpError
		if !errors.As(err, &op) {
			return fmt.Errorf("probe daemon socket: %w", err)
		}
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove stale daemon socket: %w", err)
	}
	return nil
}
