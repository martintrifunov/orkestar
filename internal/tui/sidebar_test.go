package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/martintrifunov/orkestar/internal/daemon"
	"github.com/martintrifunov/orkestar/internal/workflow"
)

func TestSidebarRowSubstitution(t *testing.T) {
	values := map[string]string{"status": "pending", "title": "Ship it"}
	if got := sidebarRow("{status}  {title}", values); got != "pending  Ship it" {
		t.Fatalf("unexpected row: %q", got)
	}
	// A token with no value renders empty rather than a literal placeholder.
	if got := sidebarRow("[{branch}] {title}", values); got != "[] Ship it" {
		t.Fatalf("missing token was not empty: %q", got)
	}
	// An unterminated brace is text, not a token to swallow.
	if got := sidebarRow("a {title", values); got != "a {title" {
		t.Fatalf("unterminated token was eaten: %q", got)
	}
}

// The three sections render their configured templates with the values the
// built-in rows use.
func TestSidebarTemplatesRender(t *testing.T) {
	m := Model{width: 160, height: 42}
	m.machines = []Machine{{ID: "local", Label: "Local"}}
	m.settings.Sidebar = sidebarTemplates{
		Task:    "{status} · {title} · {branch}",
		Agent:   "{adapter}/{state} on {task} [{machine}]",
		Session: "{id} {state}: {command}",
	}
	m.snapshot.Tasks = []workflow.Task{{
		ID: "task_1", Title: "Ship it", Status: workflow.StatusPending,
		WorktreeBranch: "task/ship", WorktreePath: "/work/ship",
	}}
	m.snapshot.Agents = []daemon.Agent{{
		ID: "agent_1", Adapter: "claude-code", State: "working", TaskID: "task_1",
	}}
	m.snapshot.Terminals = []daemon.Terminal{{
		ID: "term_1", State: "running", Command: []string{"go", "test"},
	}}

	if out := m.renderTasks(); !strings.Contains(out, "pending · Ship it · task/ship") {
		t.Fatalf("task row ignores its template:\n%s", out)
	}
	if out := m.renderAgents(); !strings.Contains(out, "claude-code/working on Ship it [Local]") {
		t.Fatalf("agent row ignores its template:\n%s", out)
	}
	if out := m.renderTerminals(); !strings.Contains(out, "term_1 running: go test") {
		t.Fatalf("session row ignores its template:\n%s", out)
	}
}

// The built-in rows are unchanged when no template is set.
func TestSidebarDefaultsWithoutTemplates(t *testing.T) {
	m := Model{width: 160, height: 42}
	m.snapshot.Tasks = []workflow.Task{{ID: "task_1", Title: "Ship it", Status: workflow.StatusPending}}
	m.snapshot.Terminals = []daemon.Terminal{{ID: "term_1", State: "running", Command: []string{"sh"}}}

	if out := m.renderTasks(); !strings.Contains(out, "pending      Ship it") {
		t.Fatalf("default task row changed:\n%s", out)
	}
	if out := m.renderTerminals(); !strings.Contains(out, "running    sh") {
		t.Fatalf("default session row changed:\n%s", out)
	}
}

// A token that does not exist is reported once at startup, like a refused key
// binding.
func TestSidebarUnknownTokenIsReported(t *testing.T) {
	complaints := sidebarComplaints(sidebarTemplates{Agent: "{adapter} {bogus}"})
	if len(complaints) != 1 || !strings.Contains(complaints[0], "bogus") {
		t.Fatalf("unknown token not reported: %v", complaints)
	}

	config := filepath.Join(t.TempDir(), "tui.json")
	if err := os.WriteFile(config, []byte(`{"sidebar":{"session":"{state} {nope}"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ORKESTAR_TUI_CONFIG", config)
	m := New(nil, t.TempDir())
	if !strings.Contains(m.notice, "Sidebar rows") || !strings.Contains(m.notice, "nope") {
		t.Fatalf("startup did not report the bad token: %q", m.notice)
	}
}
