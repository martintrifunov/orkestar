package daemonclient

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

func configureDaemon(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP, HideWindow: true}
}
