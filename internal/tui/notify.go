package tui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/martintrifunov/orkestar/internal/notify"
)

// The bell reaches someone sitting in front of the terminal. A desktop
// notification reaches them when they are not, which is the case the two
// moments worth signalling are almost always in: a task finishes, or an agent
// stops with its work unfinished, precisely while nobody is watching the pane.
//
// With a client attached the interface owns delivery, because it knows
// whether the terminal is focused; the daemon posts only when no client is
// attached at all. Where the platform can report which action was chosen, the
// notification offers to focus the pane it is about.

// notificationsSupported reports whether this system can post one, so the
// settings screen can say so rather than offering a switch that does nothing.
func notificationsSupported() bool { return notify.Supported() }

// notificationActionMsg reports that a user chose to act on a notification.
type notificationActionMsg struct {
	terminalID string
}

// notifyCmd posts a notification, waiting for an action only where the
// platform can report one and there is a pane to focus. It runs off the
// update loop; a waiting helper must not block the interface.
func notifyCmd(notice, terminalID string) tea.Cmd {
	return func() tea.Msg {
		if terminalID != "" && notify.Actionable() {
			if notify.PostAction("Orkestar", notice, "Focus pane") {
				return notificationActionMsg{terminalID: terminalID}
			}
			return nil
		}
		notify.Post("Orkestar", notice)
		return nil
	}
}

// announce is what a change worth interrupting for produces: the bell for
// someone present, a notification for someone who is not. terminalID names
// the pane the notice is about, so an actionable notification can focus it.
//
// The two are separate on purpose. The bell is cheap and local and says "glance
// up"; a notification is for a person in another window and would be noise if
// it fired every time while they were reading the pane it is about.
func (m Model) announce(notice, terminalID string) tea.Cmd {
	commands := []tea.Cmd{m.ring()}
	if m.shouldNotify() {
		commands = append(commands, notifyCmd(notice, terminalID))
	}
	return tea.Batch(commands...)
}

// shouldNotify is the decision on its own, so it can be checked without
// reaching inside a tea.Batch, which does not say what it holds.
func (m Model) shouldNotify() bool {
	return m.settings.notificationsEnabled() && !m.focused && notificationsSupported()
}
