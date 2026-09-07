package opencode_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/martintrifunov/orkestar/internal/agent"
	"github.com/martintrifunov/orkestar/internal/agent/opencode"
)

// fixtureServer is a minimal stand-in for `opencode serve` covering the
// endpoints the adapter uses: POST/GET /session, POST /session/:id/message,
// POST /session/:id/abort.
type fixtureServer struct {
	mu        sync.Mutex
	sessions  map[string]bool
	prompts   []string
	abortedID string
}

func newFixtureServer(t *testing.T) (*httptest.Server, *fixtureServer) {
	t.Helper()
	fixture := &fixtureServer{sessions: make(map[string]bool)}

	mux := http.NewServeMux()
	mux.HandleFunc("/session", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			fixture.mu.Lock()
			id := "ses_1"
			fixture.sessions[id] = true
			fixture.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"id": id})
		case http.MethodGet:
			fixture.mu.Lock()
			ids := make([]map[string]string, 0, len(fixture.sessions))
			for id := range fixture.sessions {
				ids = append(ids, map[string]string{"id": id})
			}
			fixture.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(ids)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
	mux.HandleFunc("/session/ses_1/message", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		fixture.mu.Lock()
		var sent string
		if len(body.Parts) > 0 {
			sent = body.Parts[0].Text
			fixture.prompts = append(fixture.prompts, sent)
		}
		fixture.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"info": map[string]string{},
			"parts": []map[string]string{
				{"type": "text", "text": "echo: " + sent},
			},
		})
	})
	mux.HandleFunc("/session/ses_1/abort", func(w http.ResponseWriter, r *http.Request) {
		fixture.mu.Lock()
		fixture.abortedID = "ses_1"
		fixture.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(true)
	})

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server, fixture
}

// fixtureExecutable writes a small shell script that behaves enough like an
// interactive CLI (reads stdin, ignores it, exits when its PTY closes) to
// exercise interactive mode without depending on a real OpenCode install.
func fixtureExecutable(t *testing.T) string {
	t.Helper()

	directory := t.TempDir()
	path := filepath.Join(directory, "opencode")
	script := "#!/bin/sh\ncat >/dev/null\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fixture executable: %v", err)
	}
	return path
}

func TestAdapterCapabilities(t *testing.T) {
	t.Parallel()

	capabilities := opencode.New("", "", nil).Capabilities()
	if capabilities.Name != "opencode" {
		t.Fatalf("unexpected adapter name: %q", capabilities.Name)
	}
	if !capabilities.SupportsInteractive || !capabilities.SupportsManaged || !capabilities.SupportsPrompt ||
		!capabilities.SupportsInterrupt || !capabilities.SupportsResume {
		t.Fatalf("unexpected capabilities: %#v", capabilities)
	}
}

func TestAdapterRejectsUnknownMode(t *testing.T) {
	t.Parallel()

	adapter := opencode.New("", "", nil)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := adapter.Launch(ctx, agent.LaunchOptions{Mode: "bogus"}); err == nil {
		t.Fatal("expected an unknown mode to be rejected")
	}
}

func TestAdapterInteractiveLaunchPromptAndInterrupt(t *testing.T) {
	t.Parallel()

	adapter := opencode.New(fixtureExecutable(t), "", nil)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	session, err := adapter.Launch(ctx, agent.LaunchOptions{
		Mode:      agent.ModeInteractive,
		Directory: t.TempDir(),
		Columns:   80,
		Rows:      24,
	})
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	defer session.Close()

	waitForState(t, session, agent.StateReady)

	if err := session.Prompt(ctx, "hello"); err != nil {
		t.Fatalf("prompt: %v", err)
	}
	if err := session.Interrupt(ctx); err != nil {
		t.Fatalf("interrupt: %v", err)
	}

	processSession, ok := session.(agent.ProcessSession)
	if !ok {
		t.Fatal("expected interactive session to implement agent.ProcessSession")
	}
	if err := processSession.Process().Close(); err != nil {
		t.Fatalf("close process: %v", err)
	}
	waitForState(t, session, agent.StateStopped)
}

func TestAdapterInteractiveAcceptsResume(t *testing.T) {
	t.Parallel()

	adapter := opencode.New(fixtureExecutable(t), "", nil)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	session, err := adapter.Launch(ctx, agent.LaunchOptions{Mode: agent.ModeInteractive, ResumeSessionID: "prior"})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if session.NativeSessionID() != "prior" {
		t.Fatal("resume identity not retained")
	}

}

func TestAdapterLaunchPromptAndInterrupt(t *testing.T) {
	t.Parallel()

	server, fixture := newFixtureServer(t)
	adapter := opencode.New("", server.URL, server.Client())
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	session, err := adapter.Launch(ctx, agent.LaunchOptions{Mode: agent.ModeManaged})
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	defer session.Close()

	if session.ID() == "" || session.NativeSessionID() != session.ID() {
		t.Fatalf("unexpected session IDs: id=%q native=%q", session.ID(), session.NativeSessionID())
	}
	waitForState(t, session, agent.StateReady)

	if err := session.Prompt(ctx, "hello"); err != nil {
		t.Fatalf("prompt: %v", err)
	}
	waitForState(t, session, agent.StateWorking)
	waitForState(t, session, agent.StateWaitingInput)

	fixture.mu.Lock()
	prompts := append([]string(nil), fixture.prompts...)
	fixture.mu.Unlock()
	if len(prompts) != 1 || prompts[0] != "hello" {
		t.Fatalf("unexpected prompts recorded: %#v", prompts)
	}

	if err := session.Interrupt(ctx); err != nil {
		t.Fatalf("interrupt: %v", err)
	}
	waitForState(t, session, agent.StateWaitingInput)

	fixture.mu.Lock()
	aborted := fixture.abortedID
	fixture.mu.Unlock()
	if aborted != "ses_1" {
		t.Fatalf("expected session ses_1 to be aborted, got %q", aborted)
	}
}

func TestSessionPromptForResponse(t *testing.T) {
	t.Parallel()

	server, _ := newFixtureServer(t)
	adapter := opencode.New("", server.URL, server.Client())
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	session, err := adapter.Launch(ctx, agent.LaunchOptions{Mode: agent.ModeManaged})
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	defer session.Close()

	responsive, ok := session.(agent.ResponsiveSession)
	if !ok {
		t.Fatal("expected opencode session to implement agent.ResponsiveSession")
	}
	reply, err := responsive.PromptForResponse(ctx, "hello")
	if err != nil {
		t.Fatalf("prompt for response: %v", err)
	}
	if reply != "echo: hello" {
		t.Fatalf("unexpected reply: %q", reply)
	}
}

func TestAdapterResumeRequiresExistingSession(t *testing.T) {
	t.Parallel()

	server, _ := newFixtureServer(t)
	adapter := opencode.New("", server.URL, server.Client())
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	if _, err := adapter.Launch(ctx, agent.LaunchOptions{
		Mode:            agent.ModeManaged,
		ResumeSessionID: "does-not-exist",
	}); err == nil {
		t.Fatal("expected resume of unknown session to fail")
	}
}

func waitForState(t *testing.T, session agent.Session, want agent.State) {
	t.Helper()

	deadline := time.After(5 * time.Second)
	var seen []string
	for {
		select {
		case event, ok := <-session.Events():
			if !ok {
				t.Fatalf("events channel closed before reaching state %q, saw %v", want, seen)
			}
			seen = append(seen, fmt.Sprintf("%v(%s)", event.State, event.Reason))
			if event.State == want {
				return
			}
		case <-deadline:
			// Report what did arrive: a wrong terminal state and no state at
			// all need different fixes, and this only fails under CI load.
			t.Fatalf("timed out waiting for state %q; saw %v, session reports %v", want, seen, session.State())
		}
	}
}
