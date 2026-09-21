package policy_test

import (
	"path/filepath"
	"testing"

	"github.com/martintrifunov/orkestar/internal/policy"
)

func parse(t *testing.T, body string) policy.Policy {
	t.Helper()
	parsed, err := policy.Parse([]byte(body))
	if err != nil {
		t.Fatalf("parse policy: %v", err)
	}
	return parsed
}

func TestFirstMatchWins(t *testing.T) {
	t.Parallel()

	rules := parse(t, `{"rules":[
		{"agent":"claude-code","tool":"Bash","match":"git status*","decision":"allow"},
		{"agent":"claude-code","tool":"Bash","match":"git push*","decision":"deny"},
		{"agent":"claude-code","tool":"Bash","match":"git *","decision":"ask"}
	]}`)

	if decision, _ := rules.Decide("claude-code", "Bash", "git status --short"); decision != policy.Allow {
		t.Fatalf("expected allow, got %s", decision)
	}
	if decision, _ := rules.Decide("claude-code", "Bash", "git push origin main"); decision != policy.Deny {
		t.Fatalf("expected deny, got %s", decision)
	}
	if decision, _ := rules.Decide("claude-code", "Bash", "git log"); decision != policy.Ask {
		t.Fatalf("expected ask, got %s", decision)
	}
	if decision, _ := rules.Decide("claude-code", "Read", "git status"); decision != policy.Ask {
		t.Fatalf("a different tool should not match, got %s", decision)
	}
	if decision, _ := rules.Decide("codex", "Bash", "git status"); decision != policy.Ask {
		t.Fatalf("a different agent should not match, got %s", decision)
	}
}

func TestEmptyFieldsMatchAnything(t *testing.T) {
	t.Parallel()

	rules := parse(t, `{"rules":[{"tool":"Read","decision":"allow"}]}`)
	if decision, _ := rules.Decide("anything", "Read", "/tmp/file"); decision != policy.Allow {
		t.Fatalf("expected allow, got %s", decision)
	}
}

func TestDescribeNamesTheRule(t *testing.T) {
	t.Parallel()

	rules := parse(t, `{"rules":[{"agent":"claude-code","tool":"Bash","match":"git status*","decision":"allow"}]}`)
	_, description := rules.Decide("claude-code", "Bash", "git status")
	if description == "" {
		t.Fatal("a matching rule should be describable for the audit")
	}
}

func TestParseRejectsUnsafeOrInvalidRules(t *testing.T) {
	t.Parallel()

	for name, body := range map[string]string{
		"unknown field":      `{"rules":[{"tool":"Bash","decision":"allow","note":"x"}]}`,
		"missing decision":   `{"rules":[{"tool":"Bash"}]}`,
		"invalid decision":   `{"rules":[{"tool":"Bash","decision":"maybe"}]}`,
		"matches everything": `{"rules":[{"decision":"allow"}]}`,
		"trailing data":      `{"rules":[]} {}`,
	} {
		if _, err := policy.Parse([]byte(body)); err == nil {
			t.Fatalf("%s: expected a rejection", name)
		}
	}
}

func TestLoadFileTreatsMissingAsEmpty(t *testing.T) {
	t.Parallel()

	loaded, err := policy.LoadFile(filepath.Join(t.TempDir(), "policy.json"))
	if err != nil {
		t.Fatalf("a missing policy is not an error: %v", err)
	}
	if len(loaded.Rules) != 0 {
		t.Fatalf("expected no rules, got %d", len(loaded.Rules))
	}
}
