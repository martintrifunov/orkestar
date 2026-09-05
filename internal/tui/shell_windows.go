package tui

import "os/exec"

func platformShell() string {
	if path, err := exec.LookPath("pwsh.exe"); err == nil {
		return path
	}
	return "powershell.exe"
}
