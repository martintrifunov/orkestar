//go:build !windows

package tui

func platformShell() string { return "/bin/sh" }
