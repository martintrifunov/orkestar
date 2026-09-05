package ipc

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOpenStreamCancellationInterruptsHandshake(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "orkestar-ipc-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "s")
	listener, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			accepted <- conn
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := NewClient(path).OpenStream(ctx, "terminal.attach", nil, nil); done <- err }()
	var conn net.Conn
	select {
	case conn = <-accepted:
	case <-time.After(time.Second):
		t.Fatal("connection not accepted")
	}
	defer conn.Close()
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled handshake succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("handshake ignored cancellation")
	}
}

func TestStreamWriteHasDeadline(t *testing.T) {
	client, peer := net.Pipe()
	defer client.Close()
	defer peer.Close()
	s := &Stream{connection: client, encoder: json.NewEncoder(client), timeout: 20 * time.Millisecond}
	done := make(chan error, 1)
	go func() { done <- s.Send(map[string]string{"command": "input"}) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("unread write succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("stream write blocked indefinitely")
	}
}
