package terminal

import (
	"io"
	"testing"
	"time"
)

func TestScreenQueriesAndNegotiatedInput(t *testing.T) {
	s := NewScreen(80, 24)
	defer s.Close()
	// Includes cursor position, a mode query, and bracketed paste. These replies must never escape to the host TTY.
	for _, tc := range []struct{ output, input, want string }{
		{"\x1b[6n", "", "\x1b[1;1R"},
		{"\x1b[?2004$p", "", "\x1b[?2004;2$y"},
		{"\x1b[?2004h", "hello\nworld", "\x1b[200~hello\nworld\x1b[201~"},
	} {
		t.Run(tc.want, func(t *testing.T) {
			result := make(chan string, 1)
			go func() { b := make([]byte, len(tc.want)); _, _ = io.ReadFull(s, b); result <- string(b) }()
			go func() {
				_, _ = s.Write([]byte(tc.output))
				if tc.input != "" {
					s.Paste(tc.input)
				}
			}()
			select {
			case got := <-result:
				if got != tc.want {
					t.Fatalf("got %q, want %q", got, tc.want)
				}
			case <-time.After(time.Second):
				t.Fatal("terminal reply or paste blocked")
			}
		})
	}
}

func TestScreenCloseUnblocksQueryWithoutReader(t *testing.T) {
	s := NewScreen(80, 24)
	done := make(chan struct{})
	go func() { _, _ = s.Write([]byte("\x1b[5n")); close(done) }()
	_ = s.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("close did not release query writer")
	}
}
