//go:build !windows

package daemonclient

import (
	"os/exec"
	"syscall"
)

func configureDaemon(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
