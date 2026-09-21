//go:build !darwin && !windows

package notify

import "os/exec"

// resolve uses notify-send, the freedesktop standard helper. It is not always
// installed, and its absence is not an error worth reporting: the bell still
// rings and the sidebar still says what happened.
//
// Its --wait --action form reports which action the user chose, which is what
// makes a notification something to act on rather than read.
func resolve() notifier {
	path, err := exec.LookPath("notify-send")
	if err != nil {
		return notifier{}
	}
	// Arguments, not a script, so the text needs no escaping.
	return notifier{
		program: path,
		arguments: func(title, body string) []string {
			return []string{"--app-name=Orkestar", title, body}
		},
		actionArguments: func(title, body, label string) []string {
			return []string{"--app-name=Orkestar", "--wait", "--action=default=" + label, title, body}
		},
	}
}
