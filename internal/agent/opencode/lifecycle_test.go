package opencode

import (
	"context"
	"github.com/martintrifunov/orkestar/internal/agent"
	"sync"
	"testing"
)

func TestConcurrentLifecycleAndClose(t *testing.T) {
	for range 100 {
		lifetime, cancel := context.WithCancel(context.Background())
		s := &Session{lifetime: lifetime, cancel: cancel, events: make(chan agent.LifecycleEvent, 16)}
		var group sync.WaitGroup
		group.Go(func() {
			for range 100 {
				s.emit(agent.StateWorking, "working")
			}
		})
		group.Go(func() { _ = s.Close() })
		group.Wait()
		var last agent.State
		for event := range s.Events() {
			last = event.State
		}
		if last != agent.StateStopped {
			t.Fatalf("last event: %s", last)
		}
		if _, err := s.PromptForResponse(context.Background(), "after close"); err == nil {
			t.Fatal("closed session accepted prompt")
		}
	}
}
