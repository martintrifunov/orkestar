package daemon

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/martintrifunov/orkestar/internal/agent"
	"github.com/martintrifunov/orkestar/internal/workflow"
)

type hookPermission struct {
	decision chan string
	agentID  string
	nativeID string
}
type HookInput struct {
	AgentID         string `json:"agent_id"`
	Token           string `json:"token"`
	Event           string `json:"event"`
	NativeSessionID string `json:"native_session_id"`
	Tool            string `json:"tool"`
	Notification    string `json:"notification"`
	PermissionID    string `json:"permission_id"`
}

func (s *Server) hookEvent(ctx context.Context, raw json.RawMessage) (map[string]string, error) {
	var p HookInput
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, err
	}
	s.mu.RLock()
	token := s.hookTokens[p.AgentID]
	entry := s.agents[p.AgentID]
	s.mu.RUnlock()
	if token == "" || subtle.ConstantTimeCompare([]byte(token), []byte(p.Token)) != 1 || entry == nil {
		return nil, fmt.Errorf("invalid agent hook identity")
	}
	state := agent.StateReady
	switch p.Event {
	case "SessionStart":
		state = agent.StateReady
	case "PermissionResolved":
		s.cancelHookPermissions(p.AgentID, p.PermissionID)
		state = agent.StateWorking
	case "UserPromptSubmit", "PreToolUse":
		state = agent.StateWorking
	case "Stop", "Interrupt":
		state = agent.StateWaitingInput
	case "SessionEnd":
		state = agent.StateStopped
	case "PermissionRequest":
		state = agent.StateWaitingPermission
	case "Notification":
		if p.Notification == "permission_prompt" {
			state = agent.StateWaitingPermission
		} else if p.Notification == "idle_prompt" {
			state = agent.StateWaitingInput
		} else {
			return map[string]string{}, nil
		}
	default:
		return nil, fmt.Errorf("unknown lifecycle hook %q", p.Event)
	}
	entry.mu.Lock()
	if p.NativeSessionID != "" {
		entry.metadata.NativeSessionID = p.NativeSessionID
	}
	entry.metadata.SignalSource = "hooks"
	stopped := entry.metadata.State == "stopped" || entry.metadata.State == "crashed"
	taskID := entry.metadata.TaskID
	entry.mu.Unlock()
	if stopped {
		return map[string]string{}, nil
	}
	if p.Event == "UserPromptSubmit" || p.Event == "PreToolUse" {
		s.startAgentTask(taskID)
	}
	if p.Event == "Stop" || p.Event == "Interrupt" || p.Event == "SessionEnd" {
		s.cancelHookPermissions(p.AgentID, "")
	}
	reason := ""
	if isAttentionState(state) {
		reason = p.Event
		if p.Tool != "" {
			reason += " · " + p.Tool
		}
	}
	_, _ = entry.applyLifecycle(agent.LifecycleEvent{State: state, Reason: reason, Timestamp: time.Now().UTC()}, true)
	if p.Event != "PermissionRequest" {
		if err := s.persist(); err != nil {
			return nil, err
		}
		return map[string]string{}, nil
	}
	id, err := newID("perm")
	if err != nil {
		return nil, err
	}
	pending := &hookPermission{decision: make(chan string, 1), agentID: p.AgentID, nativeID: p.PermissionID}
	s.mu.Lock()
	s.pendingHooks[id] = pending
	s.permissions[id] = PermissionRequest{ID: id, AgentID: p.AgentID, Reason: reason, CreatedAt: time.Now().UTC()}
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.pendingHooks, id); delete(s.permissions, id); s.mu.Unlock() }()
	select {
	case decision := <-pending.decision:
		if decision != "" {
			entry.applyLifecycleIf(agent.LifecycleEvent{State: agent.StateWorking, Timestamp: time.Now().UTC()}, true, agent.StateWaitingPermission)
			_ = s.persist()
		}
		return map[string]string{"decision": decision}, nil
	case <-s.stop:
		return map[string]string{}, nil
	case <-ctx.Done():
		return map[string]string{}, nil
	case <-time.After(9 * time.Minute):
		return map[string]string{}, nil
	}
}

// cancelHookPermissions releases native approvals completed elsewhere or whose
// turn/process ended. An empty decision tells the hook to use native behavior.
func (s *Server) cancelHookPermissions(agentID, nativeID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, pending := range s.pendingHooks {
		if pending.agentID != agentID || nativeID != "" && pending.nativeID != nativeID {
			continue
		}
		select {
		case pending.decision <- "":
		default:
		}
		delete(s.pendingHooks, id)
		delete(s.permissions, id)
	}
}
func quoteShell(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
func (s *Server) hookOptions(id, token, adapter string) (string, []string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", nil, err
	}
	env := append(os.Environ(), "ORKESTAR_AGENT_ID="+id, "ORKESTAR_HOOK_TOKEN="+token, "ORKESTAR_HOOK_SOCKET="+s.socketPath, "ORKESTAR_EXECUTABLE="+exe)
	if adapter == "opencode" {
		path := filepath.Join(filepath.Dir(s.socketPath), "opencode-plugin.mjs")
		if err := os.WriteFile(path, []byte(openCodePlugin), 0600); err != nil {
			return "", nil, err
		}
		config := map[string]any{}
		if existing := os.Getenv("OPENCODE_CONFIG_CONTENT"); existing != "" {
			if err := json.Unmarshal([]byte(existing), &config); err != nil {
				return "", nil, fmt.Errorf("parse OpenCode inline config: %w", err)
			}
		}
		plugins, _ := config["plugin"].([]any)
		urlPath := filepath.ToSlash(path)
		if !strings.HasPrefix(urlPath, "/") {
			urlPath = "/" + urlPath
		}
		pluginURL := (&url.URL{Scheme: "file", Path: urlPath}).String()
		config["plugin"] = append(plugins, pluginURL)
		b, _ := json.Marshal(config)
		env = append(env, "OPENCODE_CONFIG_CONTENT="+string(b))
	}
	return quoteShell(exe) + " hook", env, nil
}

// startAgentTask moves an agent's task out of pending the first time that
// session does any work. Only a pending task moves: a task already in
// progress, done or cancelled means someone has said something about it that
// a prompt should not overrule.
//
// Every failure here is ignored on purpose. The task may have been removed,
// or it may be blocked on an unfinished dependency, and neither is a reason to
// fail the hook and stall the agent that sent it.
func (s *Server) startAgentTask(taskID string) {
	if taskID == "" {
		return
	}
	task, err := s.tasks.Get(taskID)
	if err != nil || task.Status != workflow.StatusPending {
		return
	}
	_, _ = s.tasks.SetStatus(taskID, workflow.StatusInProgress)
}
