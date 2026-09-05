//go:build !windows

package daemonclient

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
	"time"
)

func watchDaemon(conn net.Conn) (func(context.Context) error, func(), error) {
	raw, err := conn.(*net.UnixConn).SyscallConn()
	if err != nil {
		return nil, nil, err
	}
	var pid int
	var peerErr error
	if err := raw.Control(func(fd uintptr) { pid, peerErr = peerPID(int(fd)) }); err != nil {
		return nil, nil, err
	}
	if peerErr != nil {
		return nil, nil, peerErr
	}
	if pid <= 0 || pid == os.Getpid() {
		return nil, nil, fmt.Errorf("invalid daemon PID %d", pid)
	}
	wait := func(ctx context.Context) error {
		ticker := time.NewTicker(25 * time.Millisecond)
		defer ticker.Stop()
		for {
			err := syscall.Kill(pid, 0)
			if errors.Is(err, syscall.ESRCH) {
				return nil
			}
			if err != nil {
				return err
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-ticker.C:
			}
		}
	}
	return wait, func() {}, nil
}
