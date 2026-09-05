package workflow

// Restore installs previously validated durable records before serving requests.
func (b *Board) Restore(tasks []Task) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, t := range tasks {
		b.tasks[t.ID] = t
	}
}
func (s *ArtifactStore) Restore(artifacts []Artifact) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, a := range artifacts {
		s.artifacts[a.ID] = a
	}
}

// Resource leases and pending permissions deliberately expire at daemon restart:
// their process owners and reply channels no longer exist.
