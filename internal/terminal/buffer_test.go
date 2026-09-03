package terminal_test

import (
	"testing"

	"github.com/martintrifunov/orkestar/internal/terminal"
)

func TestBufferKeepsNewestBytes(t *testing.T) {
	t.Parallel()

	buffer := terminal.NewBuffer(8)
	buffer.Write([]byte("abcd"))
	buffer.Write([]byte("efghij"))

	if got := string(buffer.Bytes()); got != "cdefghij" {
		t.Fatalf("unexpected buffer contents %q", got)
	}

	buffer.Write([]byte("0123456789"))
	if got := string(buffer.Bytes()); got != "23456789" {
		t.Fatalf("unexpected replacement contents %q", got)
	}
}
