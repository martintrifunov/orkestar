package daemon

import (
	"context"
	"fmt"
	"time"

	"github.com/martintrifunov/orkestar/internal/workflow"
)

// autoStartTimeout bounds a single automatic launch, the way a request's
// context bounds a client-driven one.
const autoStartTimeout = 2 * time.Minute

// autoStartLoop watches the board and launches the agents tasks asked for
// with auto_start once their dependencies are done. It is what lets a
// template chain run to the end without a person or an orchestrator calling
// task.wait on each link.
func (s *Server) autoStartLoop() {
	changes, stop := s.tasks.Watch()
	defer stop()
	// Restored tasks may be startable already, and a restart must neither
	// lose a launch nor repeat one; the assignment is what makes that true.
	s.runAutoStarts()
	for {
		select {
		case <-s.stop:
			return
		case <-changes:
			s.runAutoStarts()
		}
	}
}

// runAutoStarts launches every pending task that asked to be started and has
// nothing blocking it. Scans are serialized so a burst of board changes
// cannot start the same task twice.
func (s *Server) runAutoStarts() {
	s.autoStartMu.Lock()
	defer s.autoStartMu.Unlock()
	for _, task := range s.tasks.List() {
		if !autoStartable(task) || !s.dependenciesDone(task) {
			continue
		}
		s.startAutoTask(task)
	}
}

// autoStartable reports whether a task is waiting for an automatic launch. A
// task already assigned was started, by the daemon or by a person, and is
// never started again; one carrying an error is not retried either.
func autoStartable(task workflow.Task) bool {
	return task.AutoStart && task.AutoAgent != "" && task.AutoStartError == "" &&
		task.Status == workflow.StatusPending && task.AssigneeAgentID == ""
}

// dependenciesDone reports whether every task this one depends on reached
// done. A cancelled dependency leaves this false forever, which is what halts
// the rest of a chain rather than letting it start against work that will
// never exist.
func (s *Server) dependenciesDone(task workflow.Task) bool {
	for _, dependencyID := range task.DependsOn {
		dependency, err := s.tasks.Get(dependencyID)
		if err != nil || dependency.Status != workflow.StatusDone {
			return false
		}
	}
	return true
}

// startAutoTask launches one task's agent. The caller holds autoStartMu, so
// the re-read below only has to catch a client that assigned or cancelled the
// task while the scan was running.
func (s *Server) startAutoTask(task workflow.Task) {
	current, err := s.tasks.Get(task.ID)
	if err != nil || !autoStartable(current) || !s.dependenciesDone(current) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), autoStartTimeout)
	defer cancel()

	prompt := current.AutoPrompt
	if prompt == "" {
		prompt = "You have been assigned this task: " + current.Title
		if current.Description != "" {
			prompt += "\n\n" + current.Description
		}
	}
	if _, err := s.launchForTask(ctx, current, current.AutoAgent, prompt); err != nil {
		// Recorded and not retried: a missing adapter or an unreadable
		// directory fails the same way on every board change, and retrying
		// would turn one bad declaration into a loop. A person clears the
		// error and starts the task when the cause is fixed.
		_, _ = s.tasks.SetAutoStartError(current.ID, fmt.Sprintf("auto-start %q: %v", current.AutoAgent, err))
		_ = s.persist()
		return
	}
	// The launch assigned the task, which is the durable record that it
	// happened; save it before any restart can re-read the board.
	_ = s.persist()
}
