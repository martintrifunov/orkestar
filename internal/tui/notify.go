package tui

import (
	"os/exec"

	tea "charm.land/bubbletea/v2"
)

// The bell reaches someone sitting in front of the terminal. A desktop
// notification reaches them when they are not, which is the case the two
// moments worth signalling are almost always in: a task finishes, or an agent
// stops with its work unfinished, precisely while nobody is watching the pane.
//
// This still needs a client running. Detaching entirely leaves nothing to
// notice the change, and covering that means the daemon reaching a device
// rather than a desktop, which belongs with remote access.

// notifier is the platform's way of posting a desktop notification, resolved
// once. A platform with no way to, or a missing helper, leaves it empty and
// notifications are silently skipped: a runtime that cannot notify is not a
// reason to fail anything.
type notifier struct {
	program   string
	arguments func(title, body string) []string
}

// postNotification posts one notification, if the platform can. It runs the
// helper detached and ignores the outcome, because nothing the user is doing
// should wait on, or be interrupted by, a notification daemon.
func postNotification(title, body string) {
	n := resolveNotifier()
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

// notifyCmd posts a notification off the update loop.
func notifyCmd(title, body string) tea.Cmd {
	return func() tea.Msg {
		postNotification(title, body)
		return nil
	}
}

// announce is what a change worth interrupting for produces: the bell for
// someone present, a notification for someone who is not.
//
// The two are separate on purpose. The bell is cheap and local and says "glance
// up"; a notification is for a person in another window and would be noise if
// it fired every time while they were reading the pane it is about.
func (m Model) announce(notice string) tea.Cmd {
	commands := []tea.Cmd{m.ring()}
	if m.shouldNotify() {
		commands = append(commands, notifyCmd("Orkestar", notice))
	}
	return tea.Batch(commands...)
}

// shouldNotify is the decision on its own, so it can be checked without
// reaching inside a tea.Batch, which does not say what it holds.
func (m Model) shouldNotify() bool {
	return m.settings.notificationsEnabled() && !m.focused && notificationsSupported()
}

// notificationsSupported reports whether this system can post one, so the
// settings screen can say so rather than offering a switch that does nothing.
func notificationsSupported() bool { return resolveNotifier().program != "" }
