package daemon

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"github.com/martintrifunov/orkestar/internal/policy"
	"github.com/martintrifunov/orkestar/internal/runtimepath"
)

// PolicyAuditEntry records one permission request a policy decided, so "why
// was this approved" is answerable after the fact.
type PolicyAuditEntry struct {
	At       time.Time `json:"at"`
	AgentID  string    `json:"agent_id"`
	Adapter  string    `json:"adapter"`
	Tool     string    `json:"tool"`
	Target   string    `json:"target,omitempty"`
	Decision string    `json:"decision"`
	Rule     string    `json:"rule,omitempty"`
}

// policyAuditLimit bounds the audit kept in memory and in the snapshot. It is
// a recent history rather than a log; a file would be the right home for one.
const policyAuditLimit = 200

// PolicyAuditReport is what policy.audit answers with: the recent decisions
// and any configuration problem, so a policy file with a typo is visible
// rather than silently inert.
type PolicyAuditReport struct {
	Entries []PolicyAuditEntry `json:"entries"`
	Error   string             `json:"error,omitempty"`
}

// loadPermissionPolicy combines the workspace's policy with the user-level
// one beneath it. A file that cannot be read or parsed is reported and
// skipped: an invalid policy must change nothing, never fail a permission.
func (s *Server) loadPermissionPolicy(workspaceID string) (policy.Policy, string) {
	var combined policy.Policy
	var problems []string

	if workspaceID != "" {
		s.mu.RLock()
		workspace, ok := s.workspaces[workspaceID]
		s.mu.RUnlock()
		if ok {
			path := filepath.Join(workspace.Directory, ".orkestar", "policy.json")
			if loaded, err := policy.LoadFile(path); err != nil {
				problems = append(problems, err.Error())
			} else {
				combined.Rules = append(combined.Rules, loaded.Rules...)
			}
		}
	}
	if userPath, err := runtimepath.PolicyFilePath(); err != nil {
		problems = append(problems, err.Error())
	} else if loaded, err := policy.LoadFile(userPath); err != nil {
		problems = append(problems, err.Error())
	} else {
		combined.Rules = append(combined.Rules, loaded.Rules...)
	}
	return combined, strings.Join(problems, "; ")
}

// decidePermission answers a permission request from policy. A request
// nothing matches comes back as ask, which keeps the inbox's behavior.
func (s *Server) decidePermission(metadata Agent, tool, target string) (string, string, string) {
	loaded, problem := s.loadPermissionPolicy(metadata.WorkspaceID)
	decision, rule := loaded.Decide(metadata.Adapter, tool, target)
	return string(decision), rule, problem
}

// recordPolicyAudit appends one decision, keeping only the most recent
// entries. Callers hold no lock.
func (s *Server) recordPolicyAudit(entry PolicyAuditEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.policyHistory = append(s.policyHistory, entry)
	if len(s.policyHistory) > policyAuditLimit {
		s.policyHistory = s.policyHistory[len(s.policyHistory)-policyAuditLimit:]
	}
}

// policyAudit answers policy.audit: recent decisions and whether the policy
// files themselves can be read.
func (s *Server) policyAudit(raw json.RawMessage) (PolicyAuditReport, error) {
	var params struct {
		WorkspaceID string `json:"workspace_id"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &params); err != nil {
			return PolicyAuditReport{}, err
		}
	}
	_, problem := s.loadPermissionPolicy(params.WorkspaceID)
	s.mu.RLock()
	entries := append([]PolicyAuditEntry(nil), s.policyHistory...)
	s.mu.RUnlock()
	return PolicyAuditReport{Entries: entries, Error: problem}, nil
}

// checkPolicy answers what a policy would decide for a hypothetical request,
// which is also the way to validate the files before relying on them.
func (s *Server) checkPolicy(raw json.RawMessage) (map[string]string, error) {
	var params struct {
		WorkspaceID string `json:"workspace_id"`
		Adapter     string `json:"adapter"`
		Tool        string `json:"tool"`
		Target      string `json:"target"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, err
	}
	loaded, problem := s.loadPermissionPolicy(params.WorkspaceID)
	decision, rule := loaded.Decide(params.Adapter, params.Tool, params.Target)
	return map[string]string{"decision": string(decision), "rule": rule, "error": problem}, nil
}
