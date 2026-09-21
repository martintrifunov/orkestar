package usage_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/martintrifunov/orkestar/internal/usage"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatalf("open transcript: %v", err)
	}
	defer file.Close()
	if _, err := file.WriteString(body); err != nil {
		t.Fatalf("write transcript: %v", err)
	}
}

func TestReadClaudeSumsAssistantUsage(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	write(t, path, `{"type":"user","message":{"role":"user"}}`+"\n")
	write(t, path, `{"type":"assistant","message":{"usage":{"input_tokens":10,"cache_creation_input_tokens":5,"cache_read_input_tokens":100,"output_tokens":20}}}`+"\n")
	write(t, path, `{"type":"assistant","message":{"usage":{"input_tokens":1,"output_tokens":2}}}`+"\n")

	tokens, offset, err := usage.ReadClaude(path, 0)
	if err != nil {
		t.Fatalf("read usage: %v", err)
	}
	if tokens != 138 {
		t.Fatalf("expected 138 tokens, got %d", tokens)
	}

	// A second read from the returned offset sees only what was appended.
	write(t, path, `{"type":"assistant","message":{"usage":{"output_tokens":7}}}`+"\n")
	more, offset, err := usage.ReadClaude(path, offset)
	if err != nil {
		t.Fatalf("read more usage: %v", err)
	}
	if more != 7 {
		t.Fatalf("expected 7 new tokens, got %d", more)
	}
	if again, _, err := usage.ReadClaude(path, offset); err != nil || again != 0 {
		t.Fatalf("expected a settled read to report nothing, got %d, %v", again, err)
	}
}

func TestReadClaudeLeavesAPartialLineForLater(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "transcript.jsonl")
	write(t, path, `{"type":"assistant","message":{"usage":{"output_tokens":40}}}`)
	tokens, offset, err := usage.ReadClaude(path, 0)
	if err != nil {
		t.Fatalf("read usage: %v", err)
	}
	if tokens != 0 || offset != 0 {
		t.Fatalf("a partial line was parsed: %d tokens at offset %d", tokens, offset)
	}

	write(t, path, "\n")
	tokens, _, err = usage.ReadClaude(path, offset)
	if err != nil {
		t.Fatalf("read completed line: %v", err)
	}
	if tokens != 40 {
		t.Fatalf("expected 40 tokens once the line completed, got %d", tokens)
	}
}

func TestReadCodexTakesTheHighestThreadTotal(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	write(t, path, `{"type":"event_msg","payload":{"type":"token_usage_record","thread_token_usage":{"total_tokens":100}}}`+"\n")
	write(t, path, `{"type":"event_msg","payload":{"type":"token_usage_record","thread_token_usage":{"total_tokens":250}}}`+"\n")
	write(t, path, `{"type":"event_msg","payload":{"type":"agent_message"}}`+"\n")

	tokens, _, err := usage.ReadCodex(path, 0)
	if err != nil {
		t.Fatalf("read usage: %v", err)
	}
	if tokens != 250 {
		t.Fatalf("expected the cumulative total 250, got %d", tokens)
	}
}

func TestFindCodexRollout(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	directory := filepath.Join(root, "2026", "09", "21")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatalf("create rollout directory: %v", err)
	}
	path := filepath.Join(directory, "rollout-2026-09-21T10-00-00-session-123.jsonl")
	write(t, path, "")

	found, err := usage.FindCodexRollout(root, "session-123")
	if err != nil {
		t.Fatalf("find rollout: %v", err)
	}
	if found != path {
		t.Fatalf("expected %q, got %q", path, found)
	}
	if _, err := usage.FindCodexRollout(root, "session-missing"); err == nil {
		t.Fatal("expected a missing session to be reported")
	}
}

func TestFormatTokens(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		tokens int64
		want   string
	}{
		{0, "0"},
		{999, "999"},
		{1500, "1.5k"},
		{2_500_000, "2.5M"},
	} {
		if got := usage.FormatTokens(test.tokens); got != test.want {
			t.Fatalf("FormatTokens(%d) = %q, want %q", test.tokens, got, test.want)
		}
	}
}
