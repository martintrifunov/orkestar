package workflow

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

// ArtifactKind is the category of durable output an Artifact references.
type ArtifactKind string

const (
	ArtifactDiff       ArtifactKind = "diff"
	ArtifactTestResult ArtifactKind = "test_result"
	ArtifactLog        ArtifactKind = "log"
	ArtifactScreenshot ArtifactKind = "screenshot"
	ArtifactBuild      ArtifactKind = "build"
	ArtifactReview     ArtifactKind = "review"
)

func (k ArtifactKind) valid() bool {
	switch k {
	case ArtifactDiff, ArtifactTestResult, ArtifactLog, ArtifactScreenshot, ArtifactBuild, ArtifactReview:
		return true
	default:
		return false
	}
}

// Artifact is a durable reference to an output produced while working a
// task: a diff, test result, log bundle, screenshot, build, or review
// report. It records where the content lives (Path) or, for small text
// output, the content itself (Content); it does not own storage.
type Artifact struct {
	ID        string       `json:"id"`
	TaskID    string       `json:"task_id"`
	Kind      ArtifactKind `json:"kind"`
	Label     string       `json:"label"`
	Path      string       `json:"path,omitempty"`
	Content   string       `json:"content,omitempty"`
	CreatedAt time.Time    `json:"created_at"`
}

// ArtifactStore holds artifacts in memory, indexed by task.
type ArtifactStore struct {
	mu        sync.Mutex
	artifacts map[string]Artifact
	// order records the sequence each artifact was added in. Timestamps alone
	// do not order them: two artifacts recorded in the same clock tick compare
	// equal, and ranging a map to build the slice means the winner changes
	// between calls. A reviewer verdict and the diff it judged land together
	// often enough for that to be visible.
	order map[string]uint64
	next  uint64
}

// NewArtifactStore returns an empty ArtifactStore.
func NewArtifactStore() *ArtifactStore {
	return &ArtifactStore{artifacts: make(map[string]Artifact), order: make(map[string]uint64)}
}

// sorted orders artifacts oldest first, breaking ties by the order they
// arrived. Callers must hold s.mu.
func (s *ArtifactStore) sorted(artifacts []Artifact) []Artifact {
	sort.Slice(artifacts, func(left, right int) bool {
		if !artifacts[left].CreatedAt.Equal(artifacts[right].CreatedAt) {
			return artifacts[left].CreatedAt.Before(artifacts[right].CreatedAt)
		}
		return s.order[artifacts[left].ID] < s.order[artifacts[right].ID]
	})
	return artifacts
}

// Add records a new artifact for taskID. Exactly one of path or content
// should normally be set, but neither is required, since the caller may
// still be producing the underlying output.
func (s *ArtifactStore) Add(taskID string, kind ArtifactKind, label, path, content string) (Artifact, error) {
	if taskID == "" {
		return Artifact{}, fmt.Errorf("artifact task ID is required")
	}
	if !kind.valid() {
		return Artifact{}, fmt.Errorf("invalid artifact kind %q", kind)
	}

	id, err := newID("artifact")
	if err != nil {
		return Artifact{}, err
	}
	artifact := Artifact{
		ID:        id,
		TaskID:    taskID,
		Kind:      kind,
		Label:     label,
		Path:      path,
		Content:   content,
		CreatedAt: time.Now().UTC(),
	}

	s.mu.Lock()
	s.artifacts[id] = artifact
	s.order[id] = s.next
	s.next++
	s.mu.Unlock()
	return artifact, nil
}

// Get returns a single artifact by ID.
func (s *ArtifactStore) Get(id string) (Artifact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	artifact, ok := s.artifacts[id]
	if !ok {
		return Artifact{}, fmt.Errorf("artifact %q does not exist", id)
	}
	return artifact, nil
}

// List returns every artifact, ordered by creation time.
func (s *ArtifactStore) List() []Artifact {
	s.mu.Lock()
	defer s.mu.Unlock()
	artifacts := make([]Artifact, 0, len(s.artifacts))
	for _, artifact := range s.artifacts {
		artifacts = append(artifacts, artifact)
	}
	return s.sorted(artifacts)
}

// ForTask returns every artifact recorded against taskID, ordered by
// creation time.
func (s *ArtifactStore) ForTask(taskID string) []Artifact {
	var forTask []Artifact
	for _, artifact := range s.List() {
		if artifact.TaskID == taskID {
			forTask = append(forTask, artifact)
		}
	}
	return forTask
}

func (s *ArtifactStore) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.artifacts = make(map[string]Artifact)
	s.order = make(map[string]uint64)
	s.next = 0
}
