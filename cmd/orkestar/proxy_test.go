package main

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/martintrifunov/orkestar/internal/daemon"
	"github.com/martintrifunov/orkestar/internal/ipc"
)

// pipedConn presents a subprocess's stdio as a connection, which is exactly
// what ssh hands the remote client. Running `orkestar daemon proxy` directly
// tests the far half of remote attachment without needing an sshd: ssh
// contributes the authentication and the network, and nothing else.
type pipedConn struct {
	command *exec.Cmd
	stdin   io.WriteCloser
	stdout  io.ReadCloser
}

func (c *pipedConn) Read(b []byte) (int, error)  { return c.stdout.Read(b) }
func (c *pipedConn) Write(b []byte) (int, error) { return c.stdin.Write(b) }
func (c *pipedConn) Close() error {
	_ = c.stdin.Close()
	if c.command.Process != nil {
		_ = c.command.Process.Kill()
		_, _ = c.command.Process.Wait()
	}
	return nil
}
func (c *pipedConn) LocalAddr() net.Addr              { return pipeAddr{} }
func (c *pipedConn) RemoteAddr() net.Addr             { return pipeAddr{} }
func (c *pipedConn) SetDeadline(time.Time) error      { return nil }
func (c *pipedConn) SetReadDeadline(time.Time) error  { return nil }
func (c *pipedConn) SetWriteDeadline(time.Time) error { return nil }

type pipeAddr struct{}

func (pipeAddr) Network() string { return "pipe" }
func (pipeAddr) String() string  { return "pipe" }

// A client reaching the daemon only through a proxied pipe has to be able to
// do everything a local one can, because that is all remote attachment is.
func TestDaemonProxyCarriesTheProtocol(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("find the test binary: %v", err)
	}
	runtimeDirectory, err := os.MkdirTemp("/tmp", "orkestar-proxy-")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDirectory) })

	client := ipc.NewClientWithDialer("proxy", func(ctx context.Context, _ time.Duration) (net.Conn, error) {
		command := exec.Command(executable, "daemon", "proxy")
		command.Env = append(os.Environ(),
			"ORKESTAR_TEST_PROXY=1",
			"ORKESTAR_TEST_DAEMON=1",
			"ORKESTAR_RUNTIME_DIR="+runtimeDirectory,
		)
		stdin, err := command.StdinPipe()
		if err != nil {
			return nil, err
		}
		stdout, err := command.StdoutPipe()
		if err != nil {
			return nil, err
		}
		command.Stderr = os.Stderr
		if err := command.Start(); err != nil {
			return nil, err
		}
		return &pipedConn{command: command, stdin: stdin, stdout: stdout}, nil
	})
	t.Cleanup(func() { _ = client.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var status map[string]string
	if err := client.Call(ctx, "system.ping", nil, &status); err != nil {
		t.Fatalf("ping through the proxy: %v", err)
	}
	if status["status"] != "ok" {
		t.Fatalf("unexpected ping result: %v", status)
	}

	// A second call proves the pooled connection survives a whole exchange
	// through the pipe rather than only the first one.
	var workspace daemon.Workspace
	if err := client.Call(ctx, "workspace.create", map[string]string{"directory": t.TempDir()}, &workspace); err != nil {
		t.Fatalf("create a workspace through the proxy: %v", err)
	}
	if workspace.ID == "" {
		t.Fatalf("unexpected workspace: %+v", workspace)
	}

	var snapshot daemon.Snapshot
	if err := client.Call(ctx, "system.snapshot", nil, &snapshot); err != nil {
		t.Fatalf("snapshot through the proxy: %v", err)
	}
	if len(snapshot.Workspaces) != 1 || snapshot.Workspaces[0].ID != workspace.ID {
		t.Fatalf("the proxied daemon lost the workspace: %+v", snapshot.Workspaces)
	}

	// A refusal has to cross as a refusal, not as a broken connection.
	err = client.Call(ctx, "task.create", map[string]any{"workspace_id": "nope", "title": "x"}, nil)
	var remote *ipc.RemoteError
	if !errors.As(err, &remote) {
		t.Fatalf("the daemon's error did not survive the proxy: %v", err)
	}

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer stopCancel()
	_ = client.Call(stopCtx, "system.shutdown", nil, nil)
}
