// Package notify posts desktop notifications, and — where the platform can —
// waits for the user to act on one. It is shared by the daemon and the
// interface: the daemon uses it when no client is attached, the interface when
// one is but the terminal is not focused.
package notify

import (
	"os/exec"
	"strings"
)

// notifier is the platform's way of posting a notification, resolved once. A
// platform with no way to, or a missing helper, leaves it empty and
// notifications are silently skipped: a runtime that cannot notify is not a
// reason to fail anything.
type notifier struct {
	program   string
	arguments func(title, body string) []string
	// actionArguments posts a notification with one action and prints the
	// chosen action to stdout. Nil means this platform cannot report a choice.
	actionArguments func(title, body, label string) []string
}

// Supported reports whether this system can post a notification at all, so a
// settings screen can say so rather than offering a switch that does nothing.
func Supported() bool { return resolve().program != "" }

// Actionable reports whether a chosen action can be read back. Where it
// cannot, a caller should name the pane and the key in the body instead.
func Actionable() bool { return resolve().actionArguments != nil }

// Post posts one notification. It runs the helper detached and ignores the
// outcome, because nothing the user is doing should wait on a notification
// daemon.
func Post(title, body string) {
	n := resolve()
	if n.program == "" {
		return
	}
	command := exec.Command(n.program, n.arguments(title, body)...)
	if err := command.Start(); err != nil {
		return
	}
	// Reaped so the helper does not linger as a zombie for the life of a
	// session that may run for days.
	go func() { _ = command.Wait() }()
}

// PostAction posts a notification offering one action, and reports whether the
// user chose it. It blocks until the notification is dismissed or acted on, so
// it belongs on its own goroutine. A platform without actions posts a plain
// notification and reports false.
func PostAction(title, body, label string) bool {
	n := resolve()
	if n.program == "" {
		return false
	}
	if n.actionArguments == nil {
		Post(title, body)
		return false
	}
	output, err := exec.Command(n.program, n.actionArguments(title, body, label)...).Output()
	if err != nil {
		return false
	}
	chosen := strings.TrimSpace(string(output))
	return chosen == "default" || chosen == label
}
