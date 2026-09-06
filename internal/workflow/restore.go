package workflow

import "sort"

// Restore installs previously validated durable records before serving requests.
func (b *Board) Restore(tasks []Task) {
	b.mu.Lock()
	defer b.mu.Unlock()
	// Creation order is not persisted, so rebuild a stable one from what is.
	restored := append([]Task(nil), tasks...)
	sort.Slice(restored, func(left, right int) bool {
		if !restored[left].CreatedAt.Equal(restored[right].CreatedAt) {
			return restored[left].CreatedAt.Before(restored[right].CreatedAt)
		}
		return restored[left].ID < restored[right].ID
	})
	for _, t := range restored {
		b.tasks[t.ID] = t
		b.order[t.ID] = b.next
		b.next++
	}
}
func (s *ArtifactStore) Restore(artifacts []Artifact) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// The order artifacts arrived in is not persisted, so rebuild a stable one
	// from what is: time first, then ID for anything recorded in the same
	// tick. Restored artifacts keep a fixed order across restarts, and ones
	// added afterwards follow them.
	restored := append([]Artifact(nil), artifacts...)
	sort.Slice(restored, func(left, right int) bool {
		if !restored[left].CreatedAt.Equal(restored[right].CreatedAt) {
			return restored[left].CreatedAt.Before(restored[right].CreatedAt)
		}
		return restored[left].ID < restored[right].ID
	})
	for _, a := range restored {
		s.artifacts[a.ID] = a
		s.order[a.ID] = s.next
		s.next++
	}
}

// Resource leases and pending permissions deliberately expire at daemon restart:
// their process owners and reply channels no longer exist.
