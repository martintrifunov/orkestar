package codex_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/martintrifunov/orkestar/internal/agent"
	"github.com/martintrifunov/orkestar/internal/agent/codex"
)

func TestCodexLaunchResumeAndHooks(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "codex")
	capture := filepath.Join(dir, "args")
	script := `#!/bin/sh
printf '%s\n' "$@" > "$ARG_CAPTURE"
cat >/dev/null
`
	if err := os.WriteFile(path, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	adapter := codex.New(path)
	session, err := adapter.Launch(context.Background(), agent.LaunchOptions{Mode: agent.ModeInteractive, ResumeSessionID: "native-id", HookCommand: "/safe/orkestar hook", Environment: append(os.Environ(), "ARG_CAPTURE="+capture)})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	deadline := time.Now().Add(5 * time.Second)
	for {
		b, _ := os.ReadFile(capture)
		if strings.Contains(string(b), "native-id") {
			for _, want := range []string{"hooks.SessionStart=", "hooks.PermissionRequest=", "resume\nnative-id"} {
				if !strings.Contains(string(b), want) {
					t.Fatalf("missing %q in %s", want, b)
				}
			}
			if strings.Contains(string(b), "bypass") {
				t.Fatal("adapter bypassed approval or hook trust")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("argv not captured")
		}
		time.Sleep(time.Millisecond)
	}
	if session.NativeSessionID() != "native-id" {
		t.Fatal("native resume ID lost")
	}
	if _, err := adapter.Launch(context.Background(), agent.LaunchOptions{Mode: agent.ModeManaged}); err == nil {
		t.Fatal("unsupported mode accepted")
	}
}
