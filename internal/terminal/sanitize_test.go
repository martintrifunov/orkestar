package terminal_test

import (
	"strings"
	"testing"

	"github.com/martintrifunov/orkestar/internal/terminal"
)

// firstRow is what a pane would show for the top line of the screen.
func firstRow(t *testing.T, writes ...string) string {
	t.Helper()
	screen := terminal.NewScreen(40, 3)
	for _, write := range writes {
		if _, err := screen.Write([]byte(write)); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	return strings.TrimRight(strings.Split(screen.Render(), "\n")[0], " ")
}

// A string sequence must not end on a byte that is part of a UTF-8 character.
// Claude Code sets the window title to "✳ <conversation>" when a turn
// finishes; U+2733 encodes as E2 9C B3, and a parser that reads that 9C as the
// String Terminator prints the rest of the title into the agent pane's input.
func TestStringSequencesNeverPrintTheirPayload(t *testing.T) {
	for _, test := range []struct {
		name  string
		write string
	}{
		{"osc title ascii", "\x1b]0;Claude Code\x07"},
		{"osc title with U+2733", "\x1b]0;✳ Fix the paste bug\x07"},
		{"osc title with U+00DC", "\x1b]0;Über\x07"},
		{"osc title terminated by ST", "\x1b]0;✳ Fix the paste bug\x1b\\"},
		{"osc set window title", "\x1b]2;✳ Fix the paste bug\x07"},
		{"osc working directory", "\x1b]7;file:///home/✳/repo\x07"},
		{"dcs", "\x1bP✳ payload\x1b\\"},
		{"apc", "\x1b_G✳ payload\x1b\\"},
		{"pm", "\x1b^café payload\x1b\\"},
		{"sos", "\x1bX✳ payload\x1b\\"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if row := firstRow(t, test.write); row != "" {
				t.Fatalf("string sequence printed %q onto the screen", row)
			}
		})
	}
}

// The guard runs over every write, so ordinary output must survive it,
// including the non-ASCII an agent CLI draws its box borders and spinners
// with.
func TestPrintableOutputSurvivesTheGuard(t *testing.T) {
	for _, test := range []struct{ name, write, want string }{
		{"ascii", "hello", "hello"},
		{"accented", "café", "café"},
		{"C1 lookalike bytes", "Ü Ý Þ ß", "Ü Ý Þ ß"},
		{"star", "✳ ok", "✳ ok"},
		{"box drawing", "─┐│└", "─┐│└"},
		{"emoji", "🚀 ok", "🚀 ok"},
		{"after a title", "\x1b]0;✳ title\x07done", "done"},
		{"styled", "\x1b[1mbold\x1b[m", "\x1b[1mbold\x1b[m"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if row := firstRow(t, test.write); row != test.want {
				t.Fatalf("got %q, want %q", row, test.want)
			}
		})
	}
}

// PTY output arrives in arbitrary chunks, so the guard has to hold an
// unfinished character across writes rather than judging each write alone.
func TestGuardHoldsCharactersSplitAcrossWrites(t *testing.T) {
	title := "\x1b]0;✳ Fix the paste bug\x07"
	for split := 1; split < len(title); split++ {
		if row := firstRow(t, title[:split], title[split:]); row != "" {
			t.Fatalf("split at %d printed %q onto the screen", split, row)
		}
	}
	text := "✳ ok"
	for split := 1; split < len(text); split++ {
		if row := firstRow(t, text[:split], text[split:]); row != text {
			t.Fatalf("split at %d rendered %q, want %q", split, row, text)
		}
	}
}

// Write reports the length it was handed. The guard drops bytes on the way to
// the emulator, and a caller copying PTY output would treat a short count as a
// failed write.
func TestWriteReportsTheFullLength(t *testing.T) {
	screen := terminal.NewScreen(40, 3)
	data := []byte("\x1b]0;✳ title\x07visible")
	n, err := screen.Write(data)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if n != len(data) {
		t.Fatalf("wrote %d of %d bytes", n, len(data))
	}
}
