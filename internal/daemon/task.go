package daemon

import (
	"encoding/json"
	"fmt"

	"github.com/martintrifunov/orkestar/internal/workflow"
)

func (s *Server) createTask(rawParams json.RawMessage) (workflow.Task, error) {
	var params struct {
		WorkspaceID string   `json:"workspace_id"`
		Title       string   `json:"title"`
		Description string   `json:"description"`
		DependsOn   []string `json:"depends_on"`
	}
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return workflow.Task{}, fmt.Errorf("decode task create params: %w", err)
	}

	s.mu.RLock()
	_, ok := s.workspaces[params.WorkspaceID]
	s.mu.RUnlock()
	if !ok {
		return workflow.Task{}, fmt.Errorf("workspace %q does not exist", params.WorkspaceID)
	}

	return s.tasks.Create(params.WorkspaceID, params.Title, params.Description, params.DependsOn)
}

func (s *Server) setTaskStatus(rawParams json.RawMessage) (workflow.Task, error) {
	var params struct {
		TaskID string `json:"task_id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return workflow.Task{}, fmt.Errorf("decode task update params: %w", err)
	}
	return s.tasks.SetStatus(params.TaskID, workflow.Status(params.Status))
}

func (s *Server) assignTask(rawParams json.RawMessage) (workflow.Task, error) {
	var params struct {
		TaskID  string `json:"task_id"`
		AgentID string `json:"agent_id"`
	}
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return workflow.Task{}, fmt.Errorf("decode task assign params: %w", err)
	}
	return s.tasks.Assign(params.TaskID, params.AgentID)
}
