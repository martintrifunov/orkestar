// Package usage reads token counts out of the transcripts agent CLIs write,
// so a task budget can be enforced against what an agent actually spent.
//
// Nothing here is provider-neutral: each format is parsed where its provider
// writes it, and an agent whose transcript cannot be read simply reports no
// usage, which leaves time-based budgets and the turn watchdog as the only
// limits. That is the honest fallback, not a failure.
package usage

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// maxRead bounds how much of a transcript is read in one pass. Transcripts
// grow for the life of a session, but a single pass only has to reach the
// end; if the bound is hit the offset still advances to the last complete
// line, so the next pass resumes there rather than re-reading.
const maxRead = 32 << 20

// ClaudeTokens sums the usage an assistant message reports: input, output,
// both cache buckets. Cache reads are counted because they are what a
// runaway turn actually accumulates.
func claudeLineTokens(line []byte) int64 {
	var entry struct {
		Type    string `json:"type"`
		Message *struct {
			Usage *struct {
				InputTokens              int64 `json:"input_tokens"`
				CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
				CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
				OutputTokens             int64 `json:"output_tokens"`
			} `json:"usage"`
		} `json:"message"`
	}
	if err := json.Unmarshal(line, &entry); err != nil || entry.Type != "assistant" || entry.Message == nil || entry.Message.Usage == nil {
		return 0
	}
	u := entry.Message.Usage
	return u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens + u.OutputTokens
}

// ReadClaude returns the tokens assistant messages added since offset, and
// the offset to continue from. Usage is per message, so readings are additive.
func ReadClaude(path string, offset int64) (int64, int64, error) {
	lines, next, err := readLines(path, offset)
	if err != nil {
		return 0, offset, err
	}
	var tokens int64
	for _, line := range lines {
		tokens += claudeLineTokens(line)
	}
	return tokens, next, nil
}

// codexLineTotal returns the thread's cumulative token total from a Codex
// token_usage_record, or zero for any other line.
func codexLineTotal(line []byte) int64 {
	var entry struct {
		Payload *struct {
			Type   string `json:"type"`
			Thread *struct {
				TotalTokens int64 `json:"total_tokens"`
			} `json:"thread_token_usage"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(line, &entry); err != nil || entry.Payload == nil || entry.Payload.Type != "token_usage_record" {
		return 0
	}
	if entry.Payload.Thread == nil {
		return 0
	}
	return entry.Payload.Thread.TotalTokens
}

// ReadCodex returns the highest thread total seen since offset, and the
// offset to continue from. The total is cumulative, so a reading replaces a
// lower one rather than adding to it.
func ReadCodex(path string, offset int64) (int64, int64, error) {
	lines, next, err := readLines(path, offset)
	if err != nil {
		return 0, offset, err
	}
	var total int64
	for _, line := range lines {
		if observed := codexLineTotal(line); observed > total {
			total = observed
		}
	}
	return total, next, nil
}

// readLines reads whole lines from offset onward. A trailing partial line is
// left for the next pass: a transcript being appended to can end mid-line, and
// parsing half a JSON object would silently drop the usage it carries.
func readLines(path string, offset int64) ([][]byte, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, offset, fmt.Errorf("open transcript: %w", err)
	}
	defer file.Close()
	if offset > 0 {
		if _, err := file.Seek(offset, io.SeekStart); err != nil {
			return nil, offset, fmt.Errorf("seek transcript: %w", err)
		}
	}
	data, err := io.ReadAll(io.LimitReader(file, maxRead))
	if err != nil {
		return nil, offset, fmt.Errorf("read transcript: %w", err)
	}
	lastNewline := bytes.LastIndexByte(data, '\n')
	if lastNewline < 0 {
		return nil, offset, nil
	}
	next := offset + int64(lastNewline) + 1
	raw := bytes.Split(data[:lastNewline], []byte{'\n'})
	lines := make([][]byte, 0, len(raw))
	for _, line := range raw {
		if len(bytes.TrimSpace(line)) > 0 {
			lines = append(lines, line)
		}
	}
	return lines, next, nil
}

// CodexSessionsDirectory is where Codex keeps its rollout transcripts. Tests
// point it at a fixture with ORKESTAR_CODEX_SESSIONS_DIR.
func CodexSessionsDirectory() string {
	if directory := os.Getenv("ORKESTAR_CODEX_SESSIONS_DIR"); directory != "" {
		return directory
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".codex", "sessions")
}

// FindCodexRollout locates the rollout transcript for a native session ID.
// Codex files them by date, so the ID is what identifies the file rather than
// a path the hook could carry.
func FindCodexRollout(root, sessionID string) (string, error) {
	if root == "" || sessionID == "" {
		return "", fmt.Errorf("codex sessions directory and session id are both required")
	}
	matches, err := filepath.Glob(filepath.Join(root, "*", "*", "*", "rollout-*"+sessionID+"*.jsonl"))
	if err != nil {
		return "", fmt.Errorf("find codex rollout: %w", err)
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("no codex rollout for session %q", sessionID)
	}
	sort.Strings(matches)
	return matches[len(matches)-1], nil
}

// FormatTokens renders a token count compactly for rows and reasons, where
// the exact number matters less than its size.
func FormatTokens(tokens int64) string {
	switch {
	case tokens >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(tokens)/1_000_000)
	case tokens >= 1_000:
		return fmt.Sprintf("%.1fk", float64(tokens)/1_000)
	default:
		return fmt.Sprintf("%d", tokens)
	}
}

// SourceForAdapter names the transcript format an adapter writes, and whether
// this package knows it. Only the two formats that exist are claimed.
func SourceForAdapter(adapter string) (string, bool) {
	switch strings.ToLower(adapter) {
	case "claude", "claude-code":
		return "claude", true
	case "codex":
		return "codex", true
	default:
		return "", false
	}
}
