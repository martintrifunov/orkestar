//go:build !windows

package pty

import (
	"errors"
	"os"
	"os/exec"
	"syscall"

	"github.com/charmbracelet/x/xpty"
	"golang.org/x/sys/unix"
)

func configureCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
}

// interruptedExit reports whether err says the process was killed by the
// interrupt we sent it. An exit we asked for is not a crash, and only this
// exact cause qualifies: anything else the process died of still is one.
func interruptedExit(err error) bool {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return false
	}
	status, ok := exitErr.Sys().(syscall.WaitStatus)
	return ok && status.Signaled() && status.Signal() == syscall.SIGINT
}

// isTextFileBusy reports whether err is ETXTBSY: the executable was open for
// writing elsewhere at the exact moment this tried to run it.
func isTextFileBusy(err error) bool {
	return errors.Is(err, syscall.ETXTBSY)
}

func stopProcessGroup(process *os.Process, force bool) {
	signal := syscall.SIGHUP
	if force {
		signal = syscall.SIGKILL
	}
	_ = syscall.Kill(-process.Pid, signal)
}
func prepareIO(p xpty.Pty) (terminalIO, error) {
	// Close the parent's slave descriptor. Otherwise an exited child never
	// produces EOF on the master, and closing the master first loses final output.
	u := p.(*xpty.UnixPty)
	_ = u.Slave().Close()
	fd, err := unix.Dup(int(u.Master().Fd()))
	if err != nil {
		return nil, err
	}
	unix.CloseOnExec(fd)
	if err := unix.SetNonblock(fd, true); err != nil {
		_ = unix.Close(fd)
		return nil, err
	}
	// A nonblocking descriptor registered with Go's poller supports write deadlines.
	return os.NewFile(uintptr(fd), "orkestar-pty"), nil
}

func finishPTY(xpty.Pty) {}
