package daemon

import (
	"testing"
	"time"
)

// A frame is the whole screen rather than a diff, so how often one is offered
// decides what a transport with a cost has to carry. A PTY produces a chunk
// per line; a build log would otherwise put a frame on the wire for each.
func TestFramesAreRateLimitedPerSubscriber(t *testing.T) {
	session := newTerminalSession(Terminal{ID: "t", Columns: 80, Rows: 24}, nil)
	subscriber := &terminalSubscriber{events: make(chan terminalEvent, 1), screen: true}
	session.subscribers[subscriber] = struct{}{}

	// A burst nobody is reading, as fast as output arrives.
	const publishes = 500
	for range publishes {
		session.publish()
	}
	// Only the enqueues that actually happened matter; the channel holds one.
	if session.revision != publishes {
		t.Fatalf("revision is %d after %d publishes", session.revision, publishes)
	}
	if len(subscriber.events) != 1 {
		t.Fatalf("%d frames are queued, want the one coalesced frame", len(subscriber.events))
	}
}

// Holding a frame back must never lose the last one. A subscriber with nothing
// queued is always offered one, whatever the interval says, or a screen that
// stops changing is left showing the state before it stopped.
func TestTheLastFrameIsAlwaysOffered(t *testing.T) {
	session := newTerminalSession(Terminal{ID: "t", Columns: 80, Rows: 24}, nil)
	subscriber := &terminalSubscriber{events: make(chan terminalEvent, 1), screen: true}
	session.subscribers[subscriber] = struct{}{}

	// A burst, then a reader takes what is queued, then one final change
	// immediately afterwards — inside the interval, with nothing waiting.
	for range 50 {
		session.publish()
	}
	<-subscriber.events
	session.publish()

	select {
	case event := <-subscriber.events:
		if !event.frame {
			t.Fatalf("unexpected event: %+v", event)
		}
	default:
		t.Fatal("the change after a drained queue was never offered")
	}
}

// After the interval a subscriber is offered frames again, or output would
// stop reaching a client that is keeping up.
func TestFramesResumeAfterTheInterval(t *testing.T) {
	session := newTerminalSession(Terminal{ID: "t", Columns: 80, Rows: 24}, nil)
	subscriber := &terminalSubscriber{events: make(chan terminalEvent, 1), screen: true}
	session.subscribers[subscriber] = struct{}{}

	session.publish()
	session.publish() // Skipped: one is already waiting, inside the interval.
	time.Sleep(minFramePublish + 5*time.Millisecond)
	session.publish()

	if len(subscriber.events) != 1 {
		t.Fatalf("%d frames queued", len(subscriber.events))
	}
	if subscriber.lastPublish.IsZero() {
		t.Fatal("the subscriber was never offered a frame")
	}
	if time.Since(subscriber.lastPublish) > time.Second {
		t.Fatal("the last offer is older than the burst")
	}
}
