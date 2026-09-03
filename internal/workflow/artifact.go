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
}

// NewArtifactStore returns an empty ArtifactStore.
func NewArtifactStore() *ArtifactStore {
	return &ArtifactStore{artifacts: make(map[string]Artifact)}
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
	sort.Slice(artifacts, func(left, right int) bool {
		return artifacts[left].CreatedAt.Before(artifacts[right].CreatedAt)
	})
	return artifacts
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
