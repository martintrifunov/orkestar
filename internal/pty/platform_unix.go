//go:build !windows

package pty

import (
	"os"
	"os/exec"
	"syscall"

	"github.com/charmbracelet/x/xpty"
	"golang.org/x/sys/unix"
)

func configureCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
}
func stopProcessGroup(process *os.Process, force bool) {
	signal := syscall.SIGHUP
	if force {
		signal = syscall.SIGKILL
	}
	_ = syscall.Kill(-process.Pid, signal)
}
func prepareIO(p xpty.Pty) (*os.File, error) {
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
