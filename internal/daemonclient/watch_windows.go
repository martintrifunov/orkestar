package daemonclient

import (
	"context"
	"fmt"
	"net"
	"os"

	"golang.org/x/sys/windows"
)

func watchDaemon(conn net.Conn) (func(context.Context) error, func(), error) {
	file, ok := conn.(interface{ Fd() uintptr })
	if !ok {
		return nil, nil, fmt.Errorf("named pipe has no handle")
	}
	var pid uint32
	if err := windows.GetNamedPipeServerProcessId(windows.Handle(file.Fd()), &pid); err != nil {
		return nil, nil, err
	}
	if pid == 0 || int(pid) == os.Getpid() {
		return nil, nil, fmt.Errorf("invalid daemon PID %d", pid)
	}
	process, err := windows.OpenProcess(windows.SYNCHRONIZE, false, pid)
	if err != nil {
		return nil, nil, err
	}
	wait := func(ctx context.Context) error {
		for {
			result, err := windows.WaitForSingleObject(process, 25)
			if err != nil {
				return err
			}
			if result == windows.WAIT_OBJECT_0 {
				return nil
			}
			if err := ctx.Err(); err != nil {
				return err
			}
		}
	}
	return wait, func() { _ = windows.CloseHandle(process) }, nil
}
