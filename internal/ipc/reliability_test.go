package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSSHFixture(t *testing.T) {
	mode := os.Getenv("ORKESTAR_SSH_FIXTURE")
	if mode == "" {
		return
	}
	if mode == "stall" {
		time.Sleep(time.Hour)
		os.Exit(0)
	}
	_, _ = io.Copy(os.Stdout, os.Stdin)
	os.Exit(0)
}
func fixtureSSH(t *testing.T, mode string) net.Conn {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestSSHFixture$")
	cmd.Env = append(os.Environ(), "ORKESTAR_SSH_FIXTURE="+mode)
	conn, err := startSSHCommand(cmd, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}
func TestSSHDeadlinesAndConcurrentClose(t *testing.T) {
	conn := fixtureSSH(t, "echo")
	_ = conn.SetReadDeadline(time.Now().Add(30 * time.Millisecond))
	if _, err := conn.Read(make([]byte, 1)); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("read: %v", err)
	}
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	done := make(chan error, 1)
	go func() { _, err := conn.Write([]byte("x")); done <- err }()
	b := make([]byte, 1)
	if _, err := io.ReadFull(conn, b); err != nil || string(b) != "x" {
		t.Fatalf("echo: %q %v", b, err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	for range 5 {
		group.Go(func() { _ = conn.Close() })
	}
	group.Wait()
}
func TestSSHWriteDeadline(t *testing.T) {
	conn := fixtureSSH(t, "stall")
	_ = conn.SetWriteDeadline(time.Now().Add(50 * time.Millisecond))
	if _, err := conn.Write(make([]byte, 4*1024*1024)); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("write: %v", err)
	}
}
func TestCallCancellationReleasesConnection(t *testing.T) {
	local, remote := net.Pipe()
	defer remote.Close()
	client := NewClientWithDialer("fixture", func(context.Context, time.Duration) (net.Conn, error) { return local, nil })
	defer client.Close()
	received := make(chan struct{})
	go func() { var req Request; _ = json.NewDecoder(remote).Decode(&req); close(received) }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- client.Call(ctx, "wait", nil, nil) }()
	<-received
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled call is still waiting")
	}
	if client.IdleConnections() != 0 {
		t.Fatal("cancelled connection was pooled")
	}
}

type failedWriteConn struct {
	net.Conn
	n int
}

func (c failedWriteConn) Write(p []byte) (int, error) { return min(c.n, len(p)), io.ErrUnexpectedEOF }
func TestRetryOnlyBeforeAnyRequestBytesWereWritten(t *testing.T) {
	for _, sent := range []int{0, 1} {
		t.Run(string(rune('0'+sent)), func(t *testing.T) {
			left, right := net.Pipe()
			defer right.Close()
			var dials atomic.Int32
			client := NewClientWithDialer("fixture", func(context.Context, time.Duration) (net.Conn, error) { dials.Add(1); return nil, io.EOF })
			client.idle = []*conversation{{conn: failedWriteConn{Conn: left, n: sent}}}
			_ = client.Call(context.Background(), "mutation", nil, nil)
			want := int32(0)
			if sent == 0 {
				want = 1
			}
			if dials.Load() != want {
				t.Fatalf("wrote %d bytes; retried %d times", sent, dials.Load())
			}
		})
	}
}
