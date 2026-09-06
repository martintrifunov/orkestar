package tui

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func newThrottleTerminal() *embeddedTerminal {
	return &embeddedTerminal{terminalID: "t", events: make(chan tea.Msg, 1), done: make(chan struct{})}
}

// An idle pane must repaint the moment its frame lands: throttling is about
// what a flood costs, not about adding latency to a keystroke's echo.
func TestIdlePaneRepaintsImmediately(t *testing.T) {
	term := newThrottleTerminal()
	term.notify(false, nil)
	start := time.Now()
	if _, ok := waitEmbeddedEvent(term)().(embeddedEventMsg); !ok {
		t.Fatal("expected a repaint")
	}
	if elapsed := time.Since(start); elapsed > repaintInterval {
		t.Fatalf("idle repaint waited %v", elapsed)
	}
}

// Frames arriving back to back are coalesced into one repaint per interval,
// which is what keeps sustained output from pegging the client.
func TestRepaintsAreCappedPerInterval(t *testing.T) {
	term := newThrottleTerminal()
	start := time.Now()
	repaints := 0
	for time.Since(start) < 4*repaintInterval {
		term.notify(false, nil)
		if _, ok := waitEmbeddedEvent(term)().(embeddedEventMsg); ok {
			repaints++
		}
	}
	// Five is four intervals plus the immediate first repaint; allow one more
	// for a slow scheduler rounding an interval down.
	if repaints > 6 {
		t.Fatalf("%d repaints in four intervals, want at most 6", repaints)
	}
	if repaints < 2 {
		t.Fatalf("%d repaints in four intervals, throttle is not releasing", repaints)
	}
}

// An exiting pane is drawn at once. Waiting on it would leave the last of a
// process's output on screen for no reason, or lose it behind a close.
func TestExitIsNotThrottled(t *testing.T) {
	term := newThrottleTerminal()
	term.notify(false, nil)
	waitEmbeddedEvent(term)()
	term.notify(true, nil)
	start := time.Now()
	event, ok := waitEmbeddedEvent(term)().(embeddedEventMsg)
	if !ok || !event.exited {
		t.Fatalf("expected an exit event, got %#v", event)
	}
	if elapsed := time.Since(start); elapsed > repaintInterval {
		t.Fatalf("exit waited %v", elapsed)
	}
}
