package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/martintrifunov/orkestar/internal/agent/claude"
	"github.com/martintrifunov/orkestar/internal/agent/codex"
	"github.com/martintrifunov/orkestar/internal/agent/opencode"
	"github.com/martintrifunov/orkestar/internal/daemon"
	"github.com/martintrifunov/orkestar/internal/ipc"
	"github.com/martintrifunov/orkestar/internal/store"
	"github.com/martintrifunov/orkestar/internal/terminal"
)

func TestMain(m *testing.M) {
	if os.Getenv("ORKESTAR_TEST_DAEMON") == "1" && len(os.Args) == 3 && os.Args[1] == "daemon" && os.Args[2] == "serve" {
		if err := run(os.Args[1:]); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}

	// The far half of remote attachment, run the way ssh runs it.
	if os.Getenv("ORKESTAR_TEST_PROXY") == "1" && len(os.Args) >= 3 && os.Args[1] == "daemon" && os.Args[2] == "proxy" {
		if err := run(os.Args[1:3]); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}

	// The fixture agent launches the same private bridge as a real CLI hook.
	if len(os.Args) == 2 && os.Args[1] == "hook" {
		if err := runHook(); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestInteractiveAdaptersCompleteApprovalAndResumeWorkflow(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "orkestar-workflow-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	fixture := filepath.Join(dir, "fixture")
	script := `#!/bin/sh
stty -echo
hook() {
 printf '{"hook_event_name":"%s","session_id":"fixture-native","tool_name":"fixture-tool"}' "$1" | "$ORKESTAR_EXECUTABLE" hook
}
hook SessionStart >/dev/null
printf 'fixture-ready\r\n'
while IFS= read -r line; do
 if [ "$line" = exit ]; then hook SessionEnd >/dev/null; exit 0; fi
 hook UserPromptSubmit >/dev/null
 decision=$(hook PermissionRequest)
 case "$decision" in
  *allow*) printf 'fixture-allowed\r\n';;
  *deny*) printf 'fixture-denied\r\n';;
  *) printf 'fixture-native-fallback\r\n';;
 esac
 hook Stop >/dev/null
done
`
	if err := os.WriteFile(fixture, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(dir, "socket")
	s := daemon.NewServer(socket)
	s.RegisterAdapter(claude.New(fixture))
	s.RegisterAdapter(codex.New(fixture))
	s.RegisterAdapter(opencode.New(fixture, "", nil))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx) }()
	defer func() {
		cancel()
		select {
		case e := <-done:
			if e != nil {
				t.Error(e)
			}
		case <-time.After(5 * time.Second):
			t.Error("daemon did not stop")
		}
	}()
	client := ipc.NewClient(socket)
	call := func(method string, params, result any) {
		t.Helper()
		ctx, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		if err := client.Call(ctx, method, params, result); err != nil {
			t.Fatalf("%s: %v", method, err)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		ctx, c := context.WithTimeout(context.Background(), 100*time.Millisecond)
		err := client.Call(ctx, "system.ping", nil, nil)
		c()
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("startup timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
	var w daemon.Workspace
	call("workspace.create", map[string]string{"directory": dir}, &w)
	for _, name := range []string{"claude-code", "codex", "opencode"} {
		t.Run(name, func(t *testing.T) {
			var a daemon.Agent
			call("agent.launch", map[string]string{"workspace_id": w.ID, "adapter": name}, &a)
			wait := func(predicate func(daemon.Snapshot) bool) daemon.Snapshot {
				t.Helper()
				deadline := time.Now().Add(8 * time.Second)
				for {
					var snapshot daemon.Snapshot
					call("system.snapshot", nil, &snapshot)
					if predicate(snapshot) {
						return snapshot
					}
					if time.Now().After(deadline) {
						t.Fatalf("workflow state timed out: agents=%+v permissions=%+v", snapshot.Agents, snapshot.Permissions)
					}
					time.Sleep(10 * time.Millisecond)
				}
			}
			stateIs := func(state string) func(daemon.Snapshot) bool {
				return func(s daemon.Snapshot) bool {
					for _, entry := range s.Agents {
						if entry.ID == a.ID {
							return entry.State == state && entry.NativeSessionID == "fixture-native" && entry.SignalSource == "hooks"
						}
					}
					return false
				}
			}
			wait(stateIs("ready"))
			for _, decision := range []string{"deny", "allow"} {
				call("agent.prompt", map[string]string{"agent_id": a.ID, "text": "fixture-turn"}, nil)
				snapshot := wait(func(s daemon.Snapshot) bool {
					for _, p := range s.Permissions {
						if p.AgentID == a.ID {
							return true
						}
					}
					return false
				})
				var permissionID string
				for _, p := range snapshot.Permissions {
					if p.AgentID == a.ID {
						permissionID = p.ID
					}
				}
				call("permission.resolve", map[string]string{"permission_id": permissionID, "decision": decision}, nil)
				wait(stateIs("waiting_input"))
				var history struct {
					Screen terminal.Frame `json:"screen"`
				}
				call("terminal.history", map[string]string{"terminal_id": a.TerminalID}, &history)
				want := "fixture-allowed"
				if decision == "deny" {
					want = "fixture-denied"
				}
				if !strings.Contains(history.Screen.Content, want) {
					t.Fatalf("real hook reply did not reach fixture: %s", history.Screen.Content)
				}
			}
			call("agent.prompt", map[string]string{"agent_id": a.ID, "text": "exit"}, nil)
			wait(stateIs("stopped"))
			var resumed daemon.Agent
			call("agent.resume", map[string]string{"agent_id": a.ID}, &resumed)
			if resumed.ID == a.ID || resumed.NativeSessionID != "fixture-native" {
				t.Fatalf("invalid native resume: %+v", resumed)
			}

		})
	}

	db, err := store.Open(filepath.Join(dir, "metadata.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	persisted, err := db.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(persisted), "fixture-turn") || strings.Contains(string(persisted), "hook_token") {
		t.Fatal("prompt or hook credential leaked into metadata")
	}
}
