package daemonclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/martintrifunov/orkestar/internal/ipc"
)

// Stop waits for the daemon process to exit, not merely for its listener to
// close: shutdown still has to stop children and flush SQLite after that.
// The OS identifies the peer, so this also works with unversioned daemons.
// A daemon that is not running (no socket, or a stale one) is not an error:
// stopping is already done.
func Stop(ctx context.Context, path string) error {
	conn, err := ipc.Dial(ctx, path, time.Second)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ECONNREFUSED) {
			return nil
		}
		return fmt.Errorf("connect to daemon for shutdown: %w", err)
	}
	defer conn.Close()
	wait, release, err := watchDaemon(conn)
	if err != nil {
		return fmt.Errorf("identify daemon process: %w", err)
	}
	defer release()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	if err := json.NewEncoder(conn).Encode(ipc.Request{ID: "shutdown", Version: ipc.Version, Method: "system.shutdown"}); err != nil {
		return fmt.Errorf("send daemon shutdown: %w", err)
	}
	var response ipc.Response
	err = json.NewDecoder(conn).Decode(&response)
	// Older servers can close or reset the connection before acknowledging.
	// Once the request was sent, process exit is the authoritative result.
	if err == nil && response.Error != nil {
		return fmt.Errorf("daemon shutdown: %s", response.Error.Message)
	}
	if waitErr := wait(ctx); waitErr != nil {
		return fmt.Errorf("wait for daemon shutdown: %w", errors.Join(err, waitErr))
	}
	return nil
}
