//go:build !darwin && !windows

package tui

import "os/exec"

// resolveNotifier uses notify-send, the freedesktop standard helper. It is not
// always installed, and its absence is not an error worth reporting: the bell
// still rings and the sidebar still says what happened.
func resolveNotifier() notifier {
	path, err := exec.LookPath("notify-send")
	if err != nil {
		return notifier{}
	}
	// Arguments, not a script, so the text needs no escaping.
	return notifier{program: path, arguments: func(title, body string) []string {
		return []string{"--app-name=Orkestar", title, body}
	}}
}
