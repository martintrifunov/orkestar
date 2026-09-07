package pty

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// TestStartRetriesOnTextFileBusy pins the fix for the CI flake where a
// fixture written and executed in the same instant occasionally raced
// something else (a scanner, or the write itself) still holding the file
// open, and exec failed with ETXTBSY. It reproduces that race directly by
// holding the executable open for writing until Start is already retrying.
func TestStartRetriesOnTextFileBusy(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("ETXTBSY on an open-for-write descriptor is a Linux-specific guarantee")
	}

	directory := t.TempDir()
	path := filepath.Join(directory, "fixture")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	writer, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(2 * textFileBusyDelay)
		_ = writer.Close()
	}()

	process, err := Start(StartOptions{Command: path})
	if err != nil {
		t.Fatalf("start did not retry past ETXTBSY: %v", err)
	}
	defer process.Close()
}

// TestStartGivesUpOnAPersistentTextFileBusy pins the other half: this must
// not retry forever against a file that never becomes free.
func TestStartGivesUpOnAPersistentTextFileBusy(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("ETXTBSY on an open-for-write descriptor is a Linux-specific guarantee")
	}

	directory := t.TempDir()
	path := filepath.Join(directory, "fixture")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	writer, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()

	if _, err := Start(StartOptions{Command: path}); err == nil {
		t.Fatal("start succeeded against a file that was never released")
	}
}
