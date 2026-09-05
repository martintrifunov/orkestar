package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/martintrifunov/orkestar/internal/daemon"
	"github.com/martintrifunov/orkestar/internal/daemonclient"
	"github.com/martintrifunov/orkestar/internal/ipc"
	"github.com/martintrifunov/orkestar/internal/runtimepath"
	"github.com/martintrifunov/orkestar/internal/store"
)

// The legacy fixture drops its socket before a delayed final persistence write,
// reproducing the shutdown race. It deliberately has no system.reset method.
func TestLegacyResetDaemon(t *testing.T) {
	path := os.Getenv("ORKESTAR_TEST_LEGACY_SOCKET")
	if path == "" {
		return
	}
	listener, err := ipc.Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	for {
		conn, err := listener.Accept()
		if err != nil {
			t.Fatal(err)
		}
		var req ipc.Request
		if json.NewDecoder(conn).Decode(&req) != nil {
			conn.Close()
			continue
		}
		var result any = map[string]string{"status": "ok"}
		if req.Method == "system.snapshot" {
			result = daemon.Snapshot{Workspaces: []daemon.Workspace{{ID: "legacy", Directory: filepath.Dir(path)}}}
		}
		response, _ := ipc.NewResponse(req.ID, result)
		if req.Method != "system.ping" && req.Method != "system.snapshot" && req.Method != "system.shutdown" {
			response = ipc.NewErrorResponse(req.ID, "method_not_found", "legacy method unavailable")
		}
		_ = json.NewEncoder(conn).Encode(response)
		conn.Close()
		if req.Method == "system.shutdown" {
			listener.Close()
			time.Sleep(250 * time.Millisecond)
			db, err := store.Open(filepath.Join(filepath.Dir(path), "metadata.db"))
			if err != nil {
				t.Fatal(err)
			}
			data, _ := json.Marshal(daemon.Snapshot{Workspaces: []daemon.Workspace{{ID: "late-write", Directory: filepath.Dir(path)}}})
			if err := db.Save(context.Background(), data); err != nil {
				t.Fatal(err)
			}
			db.Close()
			return
		}
	}
}

func TestResetStopsDaemonAndClearsPersistedState(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "current"
		if legacy {
			name = "legacy"
		}
		t.Run(name, func(t *testing.T) {
			directory, err := os.MkdirTemp("", "ork-reset-")
			if err != nil {
				t.Fatal(err)
			}
			// macOS's default temporary directory can exceed Unix socket limits.
			if runtime.GOOS == "darwin" && len(directory) > 65 {
				os.Remove(directory)
				directory, err = os.MkdirTemp("/tmp", "ork-reset-")
				if err != nil {
					t.Fatal(err)
				}
			}
			defer os.RemoveAll(directory)
			t.Setenv("ORKESTAR_RUNTIME_DIR", directory)
			t.Setenv("ORKESTAR_TEST_DAEMON", "1")
			paths, err := runtimepath.Resolve()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			if legacy {
				exe, _ := os.Executable()
				cmd := exec.Command(exe, "-test.run=^TestLegacyResetDaemon$")
				cmd.Env = append(os.Environ(), "ORKESTAR_TEST_LEGACY_SOCKET="+paths.Socket)
				if err := cmd.Start(); err != nil {
					t.Fatal(err)
				}
				done := make(chan error, 1)
				go func() { done <- cmd.Wait() }()
				defer func() { _ = cmd.Process.Kill(); <-done }()
				for {
					if ipc.NewClient(paths.Socket).Call(ctx, "system.ping", nil, nil) == nil {
						break
					}
					select {
					case <-ctx.Done():
						t.Fatal(ctx.Err())
					case <-time.After(10 * time.Millisecond):
					}
				}
			} else {
				if err := daemonclient.Ensure(ctx, paths); err != nil {
					t.Fatal(err)
				}
				var workspace daemon.Workspace
				if err := ipc.NewClient(paths.Socket).Call(ctx, "workspace.create", map[string]string{"directory": directory}, &workspace); err != nil {
					t.Fatal(err)
				}
			}
			defer func() {
				stopCtx, c := context.WithTimeout(context.Background(), 2*time.Second)
				defer c()
				_ = daemonclient.Stop(stopCtx, paths.Socket)
			}()
			// Preview cannot stop either generation of daemon.
			if err := runReset(paths, nil); err != nil {
				t.Fatal(err)
			}
			if err := ipc.NewClient(paths.Socket).Call(ctx, "system.ping", nil, nil); err != nil {
				t.Fatal("preview stopped daemon", err)
			}
			marker := filepath.Join(directory, "keep.txt")
			os.WriteFile(marker, []byte("keep"), 0600)
			if err := runReset(paths, []string{"--yes"}); err != nil {
				t.Fatal(err)
			}
			conn, err := ipc.Dial(ctx, paths.Socket, 100*time.Millisecond)
			if err == nil {
				conn.Close()
				t.Fatal("reset left daemon running")
			}
			db, err := store.Open(filepath.Join(directory, "metadata.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			data, err := db.Load(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var state daemon.Snapshot
			if err := json.Unmarshal(data, &state); err != nil {
				t.Fatal(err)
			}
			if len(state.Workspaces)+len(state.Terminals)+len(state.Agents)+len(state.Tasks) != 0 {
				t.Fatalf("reset retained records: %s", data)
			}
			if _, err := os.Stat(marker); err != nil {
				t.Fatal("reset removed user file", err)
			}
		})
	}
}
