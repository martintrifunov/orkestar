// Package opencode implements the OpenCode agent adapter. Interactive mode
// launches the installed `opencode` executable inside a PTY, the same way
// the claude adapter does. Managed mode instead talks to an already-running
// OpenCode HTTP server (`opencode serve`, default http://localhost:4096)
// and returns structured replies, which interactive mode cannot. Structured
// lifecycle events from the server's SSE stream are not implemented yet.
package opencode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/martintrifunov/orkestar/internal/agent"
	"github.com/martintrifunov/orkestar/internal/agent/ptysession"
)

const (
	defaultBaseURL    = "http://localhost:4096"
	defaultExecutable = "opencode"
)

// Adapter drives OpenCode either interactively in a PTY or, in managed
// mode, over its HTTP API.
type Adapter struct {
	executable string
	baseURL    string
	client     *http.Client
}

// New returns an Adapter. executable is the CLI used for interactive mode
// (empty defaults to "opencode" resolved from PATH). baseURL and client
// configure managed mode (empty baseURL defaults to
// http://localhost:4096; a nil client uses http.DefaultClient). Tests can
// point executable at a fixture script instead of a real OpenCode install.
func New(executable, baseURL string, client *http.Client) *Adapter {
	if executable == "" {
		executable = defaultExecutable
	}
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	if client == nil {
		client = http.DefaultClient
	}
	return &Adapter{executable: executable, baseURL: baseURL, client: client}
}

func (a *Adapter) Capabilities() agent.Capabilities {
	return agent.Capabilities{
		Name:                "opencode",
		SupportsInteractive: true,
		SupportsManaged:     true,
		SupportsPrompt:      true,
		SupportsInterrupt:   true,
		SupportsResume:      true,
	}
}

func (a *Adapter) Launch(ctx context.Context, options agent.LaunchOptions) (agent.Session, error) {
	if options.Mode == agent.ModeInteractive {
		if options.ResumeSessionID != "" {
			options.Arguments = append(options.Arguments, "--session", options.ResumeSessionID)
		}
		session, err := ptysession.Launch("opencode", a.executable, options)
		if err != nil {
			return nil, fmt.Errorf("opencode adapter: %w", err)
		}
		return session, nil
	}
	if options.Mode != agent.ModeManaged {
		return nil, fmt.Errorf("opencode adapter: mode %q is not supported", options.Mode)
	}

	sessionID := options.ResumeSessionID
	if sessionID == "" {
		created, err := a.createSession(ctx)
		if err != nil {
			return nil, fmt.Errorf("opencode adapter: %w", err)
		}
		sessionID = created
	} else if err := a.requireSessionExists(ctx, sessionID); err != nil {
		return nil, fmt.Errorf("opencode adapter: resume %q: %w", sessionID, err)
	}

	session := &Session{
		id:      sessionID,
		adapter: a,
		state:   agent.StateReady,
		events:  make(chan agent.LifecycleEvent, 16),
	}
	session.emit(agent.StateReady, "launched")
	return session, nil
}

func (a *Adapter) createSession(ctx context.Context) (string, error) {
	var created struct {
		ID string `json:"id"`
	}
	if err := a.doJSON(ctx, http.MethodPost, "/session", nil, &created); err != nil {
		return "", fmt.Errorf("create session: %w", err)
	}
	if created.ID == "" {
		return "", errors.New("create session: server returned no session ID")
	}
	return created.ID, nil
}

func (a *Adapter) requireSessionExists(ctx context.Context, sessionID string) error {
	var sessions []struct {
		ID string `json:"id"`
	}
	if err := a.doJSON(ctx, http.MethodGet, "/session", nil, &sessions); err != nil {
		return fmt.Errorf("list sessions: %w", err)
	}
	for _, session := range sessions {
		if session.ID == sessionID {
			return nil
		}
	}
	return fmt.Errorf("session %q not found on server", sessionID)
}

func (a *Adapter) prompt(ctx context.Context, sessionID, text string) (string, error) {
	body := map[string]any{
		"parts": []map[string]string{{"type": "text", "text": text}},
	}
	var response struct {
		Parts []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"parts"`
	}
	path := fmt.Sprintf("/session/%s/message", sessionID)
	if err := a.doJSON(ctx, http.MethodPost, path, body, &response); err != nil {
		return "", fmt.Errorf("send prompt: %w", err)
	}

	var reply strings.Builder
	for _, part := range response.Parts {
		if part.Type == "text" {
			reply.WriteString(part.Text)
		}
	}
	return reply.String(), nil
}

func (a *Adapter) abort(ctx context.Context, sessionID string) error {
	path := fmt.Sprintf("/session/%s/abort", sessionID)
	if err := a.doJSON(ctx, http.MethodPost, path, nil, nil); err != nil {
		return fmt.Errorf("abort session: %w", err)
	}
	return nil
}

func (a *Adapter) doJSON(ctx context.Context, method, path string, body, result any) error {
	var reader *bytes.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		reader = bytes.NewReader(encoded)
	} else {
		reader = bytes.NewReader(nil)
	}

	request, err := http.NewRequestWithContext(ctx, method, a.baseURL+path, reader)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}

	response, err := a.client.Do(request)
	if err != nil {
		return fmt.Errorf("call %s %s: %w", method, path, err)
	}
	defer response.Body.Close()

	if response.StatusCode >= 300 {
		return fmt.Errorf("%s %s: unexpected status %d", method, path, response.StatusCode)
	}
	if result == nil {
		return nil
	}
	if err := json.NewDecoder(response.Body).Decode(result); err != nil {
		return fmt.Errorf("decode %s %s response: %w", method, path, err)
	}
	return nil
}

// Session is a managed OpenCode agent session driven over HTTP.
type Session struct {
	id      string
	adapter *Adapter

	mu     sync.Mutex
	state  agent.State
	closed bool
	events chan agent.LifecycleEvent
}

var (
	_ agent.Session           = (*Session)(nil)
	_ agent.ResponsiveSession = (*Session)(nil)
)

func (s *Session) ID() string              { return s.id }
func (s *Session) NativeSessionID() string { return s.id }

func (s *Session) State() agent.State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state
}

// Prompt sends a message to the session and blocks until OpenCode finishes
// the turn, mirroring the server's synchronous /session/:id/message call.
func (s *Session) Prompt(ctx context.Context, text string) error {
	_, err := s.PromptForResponse(ctx, text)
	return err
}

// PromptForResponse sends a message and returns OpenCode's reply text,
// concatenating every text part of the response.
func (s *Session) PromptForResponse(ctx context.Context, text string) (string, error) {
	s.emit(agent.StateWorking, "prompted")
	reply, err := s.adapter.prompt(ctx, s.id, text)
	if err != nil {
		s.emit(agent.StateReady, "prompt failed")
		return "", fmt.Errorf("opencode session: %w", err)
	}
	s.emit(agent.StateReady, "prompt completed")
	return reply, nil
}

// Interrupt aborts the session's current turn.
func (s *Session) Interrupt(ctx context.Context) error {
	if err := s.adapter.abort(ctx, s.id); err != nil {
		return fmt.Errorf("opencode session: %w", err)
	}
	s.emit(agent.StateReady, "interrupted")
	return nil
}

func (s *Session) Events() <-chan agent.LifecycleEvent {
	return s.events
}

// Close releases the session's event channel. It does not delete the
// session on the OpenCode server; the server owns that lifecycle.
func (s *Session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.state = agent.StateStopped
	close(s.events)
	return nil
}

func (s *Session) emit(state agent.State, reason string) {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.state = state
	s.mu.Unlock()

	event := agent.LifecycleEvent{State: state, Reason: reason, Timestamp: time.Now().UTC()}
	select {
	case s.events <- event:
	default:
	}
}
