package terminal

import (
	"fmt"
	"io"
	"strings"
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

func TestHistoryIsBoundedAndDoesNotReplayQueries(t *testing.T) {
	screen := NewScreen(30, 4)
	defer screen.Close()
	for i := 0; i < 2200; i++ {
		if _, err := screen.Write([]byte(fmt.Sprintf("line-%04d\r\n", i))); err != nil {
			t.Fatal(err)
		}
	}
	history := screen.History()
	if len(history) != 2000 {
		t.Fatalf("history has %d lines", len(history))
	}
	if strings.Contains(strings.Join(history, "\n"), "line-0000") {
		t.Fatal("old history was not evicted")
	}
	if !strings.Contains(screen.Frame().ANSI(), "line-2199") {
		t.Fatal("last output missing")
	}
	if strings.Contains(screen.Frame().ANSI(), "\x1b[6n") {
		t.Fatal("replay contains query")
	}
}

func TestMouseNegotiationAndPaneCoordinates(t *testing.T) {
	s := NewScreen(80, 24)
	defer s.Close()
	_, _ = s.Write([]byte("\x1b[?1000h\x1b[?1006h"))
	if !s.Frame().Mouse {
		t.Fatal("mouse mode not published")
	}
	done := make(chan string, 1)
	go func() { b := make([]byte, 64); n, _ := s.Read(b); done <- string(b[:n]) }()
	s.Mouse("click", 2, 3, 1, 0)
	select {
	case b := <-done:
		if b != "\x1b[<0;3;4M" {
			t.Fatalf("wrong mouse report: %q", b)
		}
	case <-time.After(time.Second):
		t.Fatal("mouse report blocked")
	}
	_, _ = s.Write([]byte("\x1b[?1000l"))
	if s.Frame().Mouse {
		t.Fatal("mouse mode not cleared")
	}
}
