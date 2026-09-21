package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// policyServer builds a daemon with one workspace and one hook-identified
// agent, which is all a permission request needs.
func policyServer(t *testing.T, workspaceDirectory, body string) *Server {
	t.Helper()
	s := NewServer(filepath.Join(t.TempDir(), "socket"))
	s.workspaces["ws"] = Workspace{ID: "ws", Directory: workspaceDirectory}
	if body != "" {
		directory := filepath.Join(workspaceDirectory, ".orkestar")
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatalf("create policy directory: %v", err)
		}
		if err := os.WriteFile(filepath.Join(directory, "policy.json"), []byte(body), 0o644); err != nil {
			t.Fatalf("write policy: %v", err)
		}
	}
	s.hookTokens["a"] = "token"
	s.agents["a"] = newAgentSession(Agent{
		ID: "a", State: "working", Adapter: "claude-code", WorkspaceID: "ws",
	}, nil)
	return s
}

func permissionHook(t *testing.T, tool, target string) []byte {
	t.Helper()
	payload, err := json.Marshal(HookInput{
		AgentID: "a", Token: "token", Event: "PermissionRequest", Tool: tool, Target: target,
	})
	if err != nil {
		t.Fatalf("marshal hook: %v", err)
	}
	return payload
}

func waitForPermission(t *testing.T, s *Server) PermissionRequest {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.RLock()
		for _, request := range s.permissions {
			s.mu.RUnlock()
			return request
		}
		s.mu.RUnlock()
		time.Sleep(time.Millisecond)
	}
	t.Fatal("permission was never published")
	return PermissionRequest{}
}

func TestPolicyDecidesWithoutTheInbox(t *testing.T) {
	t.Parallel()

	for _, decision := range []string{"allow", "deny"} {
		s := policyServer(t, t.TempDir(), `{"rules":[
			{"agent":"claude-code","tool":"Bash","match":"git status*","decision":"`+decision+`"}
		]}`)
		result, err := s.hookEvent(context.Background(), permissionHook(t, "Bash", "git status --short"))
		if err != nil {
			t.Fatalf("hook: %v", err)
		}
		if result["decision"] != decision {
			t.Fatalf("expected %s, got %q", decision, result["decision"])
		}
		if permissions := s.listPermissions(); len(permissions) != 0 {
			t.Fatalf("a decided request reached the inbox: %+v", permissions)
		}
		s.mu.RLock()
		entries := append([]PolicyAuditEntry(nil), s.policyHistory...)
		s.mu.RUnlock()
		if len(entries) != 1 || entries[0].Decision != decision || entries[0].Rule == "" {
			t.Fatalf("the decision was not audited: %+v", entries)
		}
		if state := s.agents["a"].snapshot().State; state != "working" {
			t.Fatalf("a decided request should leave the agent working, got %q", state)
		}
	}
}

func TestUnmatchedPermissionStaysAQuestion(t *testing.T) {
	t.Parallel()

	s := policyServer(t, t.TempDir(), `{"rules":[{"tool":"Read","decision":"allow"}]}`)
	result := make(chan map[string]string, 1)
	go func() {
		reply, _ := s.hookEvent(context.Background(), permissionHook(t, "Bash", "rm -rf build"))
		result <- reply
	}()

	request := waitForPermission(t, s)
	if request.Policy != "no matching rule" {
		t.Fatalf("unexpected policy label: %q", request.Policy)
	}
	resolved, _ := json.Marshal(map[string]string{"permission_id": request.ID, "decision": "deny"})
	if _, err := s.resolvePermission(context.Background(), resolved); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	select {
	case reply := <-result:
		if reply["decision"] != "deny" {
			t.Fatalf("expected the human decision, got %q", reply["decision"])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the hook never returned")
	}
}

func TestInvalidPolicyChangesNothing(t *testing.T) {
	t.Parallel()

	// A rule that would match every request with allow is refused, so the
	// permission must still reach the inbox.
	s := policyServer(t, t.TempDir(), `{"rules":[{"decision":"allow"}]}`)
	result := make(chan map[string]string, 1)
	go func() {
		reply, _ := s.hookEvent(context.Background(), permissionHook(t, "Bash", "rm -rf build"))
		result <- reply
	}()

	request := waitForPermission(t, s)
	if request.Policy != "no matching rule" {
		t.Fatalf("an invalid policy decided a request: %q", request.Policy)
	}
	resolved, _ := json.Marshal(map[string]string{"permission_id": request.ID, "decision": "allow"})
	if _, err := s.resolvePermission(context.Background(), resolved); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	<-result

	report, err := s.policyAudit(json.RawMessage(`{"workspace_id":"ws"}`))
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if report.Error == "" {
		t.Fatal("an unreadable policy was not reported")
	}
}

func TestPolicyCheckReportsTheDecision(t *testing.T) {
	t.Parallel()

	s := policyServer(t, t.TempDir(), `{"rules":[{"tool":"Bash","match":"go test*","decision":"allow"}]}`)
	params, _ := json.Marshal(map[string]string{
		"workspace_id": "ws", "adapter": "claude-code", "tool": "Bash", "target": "go test ./...",
	})
	result, err := s.checkPolicy(params)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	if result["decision"] != "allow" || result["rule"] == "" {
		t.Fatalf("unexpected check result: %+v", result)
	}
}
