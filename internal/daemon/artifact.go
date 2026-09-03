package daemon

import (
	"encoding/json"
	"fmt"

	"github.com/martintrifunov/orkestar/internal/workflow"
)

func (s *Server) createArtifact(rawParams json.RawMessage) (workflow.Artifact, error) {
	var params struct {
		TaskID  string `json:"task_id"`
		Kind    string `json:"kind"`
		Label   string `json:"label"`
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return workflow.Artifact{}, fmt.Errorf("decode artifact create params: %w", err)
	}

	if _, err := s.tasks.Get(params.TaskID); err != nil {
		return workflow.Artifact{}, err
	}

	return s.artifacts.Add(params.TaskID, workflow.ArtifactKind(params.Kind), params.Label, params.Path, params.Content)
}
