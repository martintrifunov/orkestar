package pty

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"
)

func TestFinalOutputDrainsAfterExit(t *testing.T) {
	p, err := Start(StartOptions{Command: "/bin/sh", Arguments: []string{"-c", "printf final-output"}})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	// Some kernels wait for terminal output to drain before completing exit.
	// Delay the reader to exercise output produced just before process exit.
	time.Sleep(50 * time.Millisecond)
	done := make(chan []byte, 1)
	go func() { b, _ := io.ReadAll(p); done <- b }()
	select {
	case b := <-done:
		if !strings.Contains(string(b), "final-output") {
			t.Fatalf("final output lost: %q", b)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("PTY EOF blocked after child exit")
	}
}

func TestBlockedInputAndShutdownAreBounded(t *testing.T) {
	p, err := Start(StartOptions{Command: "/bin/sh", Arguments: []string{"-c", "stty raw -echo; printf ready; sleep 60"}})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	ready := make(chan struct{})
	go func() { b := make([]byte, 5); _, _ = io.ReadFull(p, b); close(ready) }()
	select {
	case <-ready:
	case <-time.After(3 * time.Second):
		t.Fatal("fixture did not start")
	}
	writeDone := make(chan error, 1)
	go func() { _, e := p.Write(bytes.Repeat([]byte("x"), 4*1024*1024)); writeDone <- e }()
	select {
	case e := <-writeDone:
		if e == nil {
			t.Fatal("expected a full PTY to time out")
		}
	case <-time.After(4 * time.Second):
		t.Fatal("blocked PTY input has no deadline")
	}
	closeDone := make(chan struct{})
	go func() { _ = p.Close(); close(closeDone) }()
	select {
	case <-closeDone:
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown blocked")
	}
	select {
	case <-p.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("process was not reaped")
	}
}
