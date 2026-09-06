package daemon

import (
	"fmt"
	"strings"
	"testing"

	"github.com/martintrifunov/orkestar/internal/terminal"
)

// buildLog is what sustained output actually looks like: many small writes,
// each one a line the PTY hands over as its own chunk.
func buildLog(lines int) [][]byte {
	chunks := make([][]byte, lines)
	for i := range chunks {
		chunks[i] = fmt.Appendf(nil, "\x1b[32m[%04d]\x1b[m compiling %s\r\n", i, strings.Repeat("package/", 8))
	}
	return chunks
}

func benchmarkSession(subscribers int) (*terminalSession, [][]byte) {
	s := newTerminalSession(Terminal{ID: "bench", Columns: 120, Rows: 40}, nil)
	for range subscribers {
		sub := &terminalSubscriber{events: make(chan terminalEvent, 1), screen: true}
		s.subscribers[sub] = struct{}{}
	}
	return s, buildLog(256)
}

// BenchmarkSustainedOutput covers the daemon's whole per-chunk cost: emulate
// the bytes, then hand every subscriber the result. Subscribers never drain,
// which is the case that matters — a client that cannot keep up must not make
// the daemon do more work per chunk.
func BenchmarkSustainedOutput(b *testing.B) {
	for _, subscribers := range []int{0, 1, 2} {
		b.Run(fmt.Sprintf("subscribers=%d", subscribers), func(b *testing.B) {
			session, chunks := benchmarkSession(subscribers)
			b.ReportAllocs()
			for i := 0; b.Loop(); i++ {
				session.mu.Lock()
				_, _ = session.screen.Write(chunks[i%len(chunks)])
				session.publish()
				session.mu.Unlock()
			}
		})
	}
}

// BenchmarkScreenWrite isolates the emulator, so the benchmark above says how
// much of the cost is publishing rather than emulating.
func BenchmarkScreenWrite(b *testing.B) {
	screen := terminal.NewScreen(120, 40)
	chunks := buildLog(256)
	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		_, _ = screen.Write(chunks[i%len(chunks)])
	}
}
