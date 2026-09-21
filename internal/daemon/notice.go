package daemon

import (
	"github.com/martintrifunov/orkestar/internal/notify"
	"github.com/martintrifunov/orkestar/internal/workflow"
)

// SetNotifier overrides how the daemon posts desktop notifications. Tests use
// it; production resolves the platform helper.
func (s *Server) SetNotifier(post func(title, body string)) { s.notifier = post }

// notifyDetached posts a desktop notification only when no client is
// attached. With a client attached the interface owns delivery, because it
// knows whether the terminal is focused and the daemon does not; without one,
// a bell that only rings in an empty room is no bell at all.
func (s *Server) notifyDetached(title, body string) {
	s.mu.RLock()
	attached := len(s.connections) > 0
	post := s.notifier
	s.mu.RUnlock()
	if attached {
		return
	}
	if post == nil {
		notify.Post(title, body)
		return
	}
	post(title, body)
}

// announceTaskDone is the detached half of the bell: a task finished while
// nobody was looking.
func (s *Server) announceTaskDone(task workflow.Task) {
	s.notifyDetached("Orkestar", "Task done: "+task.Title)
}

// announceAgentStopped is the other half: an agent stopped with its task
// still open, which is the moment nothing else announces.
func (s *Server) announceAgentStopped(metadata Agent) {
	if metadata.TaskID == "" {
		return
	}
	task, err := s.tasks.Get(metadata.TaskID)
	if err != nil || task.Status == workflow.StatusDone || task.Status == workflow.StatusCancelled {
		return
	}
	s.notifyDetached("Orkestar", metadata.Adapter+" stopped with "+task.Title+" unfinished")
}
