package ipc_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/martintrifunov/orkestar/internal/ipc"
)

// countingServer answers requests the way the daemon does — many per
// connection — and records how many connections it was given, which is the
// thing pooling is meant to reduce.
type countingServer struct {
	listener net.Listener
	mu       sync.Mutex
	accepted int
	handled  int
	// slow blocks every request until released, so a test can hold one call
	// open and watch a second get its own connection.
	slow chan struct{}
}

func newCountingServer(t *testing.T) *countingServer {
	t.Helper()
	directory, err := os.MkdirTemp("/tmp", "orkestar-pool-")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })

	path := filepath.Join(directory, "orkestar.sock")
	listener, err := ipc.Listen(path)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := &countingServer{listener: listener}
	t.Cleanup(func() { _ = listener.Close() })
	go server.serve()
	return server
}

func (s *countingServer) path() string { return s.listener.Addr().String() }

func (s *countingServer) counts() (accepted, handled int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.accepted, s.handled
}

func (s *countingServer) serve() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		s.accepted++
		s.mu.Unlock()
		go s.handle(conn)
	}
}

func (s *countingServer) handle(conn net.Conn) {
	defer conn.Close()
	scanner := bufio.NewScanner(conn)
	encoder := json.NewEncoder(conn)
	for scanner.Scan() {
		var request ipc.Request
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			return
		}
		s.mu.Lock()
		s.handled++
		s.mu.Unlock()
		if s.slow != nil {
			<-s.slow
		}
		response, _ := ipc.NewResponse(request.ID, map[string]string{"method": request.Method})
		if encoder.Encode(response) != nil {
			return
		}
	}
}

// The daemon has always read many requests from one connection. Reusing it is
// what makes a transport with a real connection cost — anything but a local
// socket — usable at the rate the TUI polls.
func TestCallsReuseAConnection(t *testing.T) {
	server := newCountingServer(t)
	client := ipc.NewClient(server.path())
	defer client.Close()

	for range 10 {
		var result map[string]string
		if err := client.Call(context.Background(), "system.ping", nil, &result); err != nil {
			t.Fatalf("call: %v", err)
		}
		if result["method"] != "system.ping" {
			t.Fatalf("unexpected result: %v", result)
		}
	}

	accepted, handled := server.counts()
	if handled != 10 {
		t.Fatalf("the server handled %d requests, want 10", handled)
	}
	if accepted != 1 {
		t.Fatalf("10 calls opened %d connections, want 1", accepted)
	}
}

// A call in flight must not block another. Each takes its own connection,
// which is why this is a pool and not one shared connection.
func TestAConcurrentCallDoesNotWaitForOne(t *testing.T) {
	server := newCountingServer(t)
	server.slow = make(chan struct{})
	client := ipc.NewClient(server.path())
	defer client.Close()

	var group sync.WaitGroup
	for range 3 {
		group.Add(1)
		go func() {
			defer group.Done()
			var result map[string]string
			if err := client.Call(context.Background(), "slow", nil, &result); err != nil {
				t.Errorf("call: %v", err)
			}
		}()
	}
	// Let all three reach the server before any is answered.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, handled := server.counts(); handled == 3 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, handled := server.counts(); handled != 3 {
		t.Fatalf("only %d of 3 calls got through; they are queueing", handled)
	}
	close(server.slow)
	group.Wait()
}

// A pooled connection can be dead by the time it is next used, most obviously
// after a daemon restart. The send fails, which proves the daemon never saw
// the request, so it is safe to send again on a fresh connection.
func TestAStaleConnectionIsRetried(t *testing.T) {
	server := newCountingServer(t)
	client := ipc.NewClient(server.path())
	defer client.Close()

	var result map[string]string
	if err := client.Call(context.Background(), "first", nil, &result); err != nil {
		t.Fatalf("first call: %v", err)
	}

	// Restart underneath the client, which leaves it holding a connection to
	// a server that no longer exists.
	path := server.path()
	_ = server.listener.Close()
	time.Sleep(50 * time.Millisecond)
	replacement, err := ipc.Listen(path)
	if err != nil {
		t.Fatalf("relisten: %v", err)
	}
	restarted := &countingServer{listener: replacement}
	defer replacement.Close()
	go restarted.serve()

	if err := client.Call(context.Background(), "second", nil, &result); err != nil {
		t.Fatalf("the call after a restart failed instead of retrying: %v", err)
	}
	if result["method"] != "second" {
		t.Fatalf("unexpected result: %v", result)
	}
}

// The pool is bounded. A client that kept every connection it ever opened
// would be a leak in a process that runs for days.
func TestIdleConnectionsAreBounded(t *testing.T) {
	server := newCountingServer(t)
	server.slow = make(chan struct{})
	client := ipc.NewClient(server.path())
	defer client.Close()

	// Twelve at once forces twelve connections; only a few may be kept.
	var group sync.WaitGroup
	for range 12 {
		group.Add(1)
		go func() {
			defer group.Done()
			var result map[string]string
			_ = client.Call(context.Background(), "ping", nil, &result)
		}()
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, handled := server.counts(); handled == 12 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	close(server.slow)
	group.Wait()

	if idle := client.IdleConnections(); idle > 4 {
		t.Fatalf("%d idle connections were kept", idle)
	}

	// And what is kept still works.
	var result map[string]string
	if err := client.Call(context.Background(), "after", nil, &result); err != nil {
		t.Fatalf("call after the burst: %v", err)
	}
}

// A daemon that is not running has to be reported as such, since that is how
// the CLI decides to start one.
func TestAMissingDaemonIsStillUnavailable(t *testing.T) {
	client := ipc.NewClient(filepath.Join(t.TempDir(), "orkestar.sock"))
	defer client.Close()

	err := client.Call(context.Background(), "system.ping", nil, nil)
	if err == nil {
		t.Fatal("calling a daemon that is not running should fail")
	}
	if !ipc.IsUnavailable(err) {
		t.Fatalf("error %v is not recognised as an unavailable daemon", err)
	}
	if !strings.Contains(err.Error(), "connect to daemon") {
		t.Fatalf("unexpected error: %v", err)
	}
}
