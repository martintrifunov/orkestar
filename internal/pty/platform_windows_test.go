package pty

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWindowsFinalOutput(t *testing.T) {
	p, err := Start(StartOptions{Command: "powershell.exe", Arguments: []string{"-NoLogo", "-NoProfile", "-Command", "Write-Output 'final-output'"}})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	done := make(chan []byte, 1)
	go func() { b, _ := io.ReadAll(p); done <- b }()
	select {
	case b := <-done:
		if !strings.Contains(string(b), "final-output") {
			t.Fatalf("lost output: %q", b)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("ConPTY failed to drain and close")
	}
}

func TestWindowsBlockedInputDeadline(t *testing.T) {
	p, err := Start(StartOptions{Command: "powershell.exe", Arguments: []string{"-NoLogo", "-NoProfile", "-Command", "Start-Sleep -Seconds 60"}})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	go io.Copy(io.Discard, p)
	done := make(chan error, 1)
	go func() { _, err := p.Write(bytes.Repeat([]byte("x"), 32*1024*1024)); done <- err }()
	select {
	case err := <-done:
		// Conhost may accept the entire write into its own input queue. If it
		// applies backpressure, our deadline must release the writer instead.
		if err != nil && !errors.Is(err, os.ErrDeadlineExceeded) {
			t.Fatalf("unexpected write error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("ConPTY write did not time out")
	}
}

func TestWindowsBatchLauncher(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent shim.cmd")
	if err := os.WriteFile(path, []byte("@echo off\r\necho batch-ready\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	p, err := Start(StartOptions{Command: path})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	done := make(chan []byte, 1)
	go func() { b, _ := io.ReadAll(p); done <- b }()
	select {
	case b := <-done:
		if !strings.Contains(string(b), "batch-ready") {
			t.Fatalf("batch shim failed: %q", b)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("batch shim did not exit")
	}
}
