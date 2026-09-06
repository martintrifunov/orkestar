package terminal

// stringGuard rewrites terminal output so that the bytes of a UTF-8 character
// are never mistaken for C1 control codes inside a string sequence (OSC, DCS,
// SOS, PM or APC).
//
// The VT parser terminates a string sequence on a bare 0x9C, treating it as
// the C1 String Terminator, even though a terminal in UTF-8 mode must not:
// 0x9C there is a continuation byte. U+2733 encodes as E2 9C B3, so the title
// Claude Code sets when a turn finishes, "\x1b]0;✳ <conversation>\a", ends
// after "✳" and the rest of the title is printed at the cursor, which is the
// agent pane's input box. That is the "conversation title appears in the
// prompt" bug. SOS, PM and APC payloads are worse: the parser leaves the
// sequence on any byte above 0x7F.
//
// The guard withholds each multi-byte character until it is complete and drops
// the ones the parser would mishandle, so the sequence stays a sequence. A
// dropped glyph costs nothing: Orkestar renders the screen, never a string
// payload. Bytes outside a string sequence are passed through untouched, and
// state carries across writes because output arrives in arbitrary chunks.
type stringGuard struct {
	state     guardState
	pending   []byte
	remaining int
	// asciiOnly records that the current string sequence is one whose payload
	// the parser only accepts as ASCII (SOS, PM, APC).
	asciiOnly bool
}

type guardState int

const (
	guardGround guardState = iota
	guardEscape
	guardString
)

// utf8Length returns the number of bytes in a UTF-8 character starting with
// lead, or 0 if lead does not start a multi-byte character. It mirrors the
// lead-byte ranges the parser itself recognizes.
func utf8Length(lead byte) int {
	switch {
	case lead >= 0xC2 && lead <= 0xDF:
		return 2
	case lead >= 0xE0 && lead <= 0xEF:
		return 3
	case lead >= 0xF0 && lead <= 0xF4:
		return 4
	default:
		return 0
	}
}

// filter returns data with unrepresentable string-sequence characters removed.
// It returns data itself when there is nothing to change, which is every write
// that carries no string sequence at all.
func (g *stringGuard) filter(data []byte) []byte {
	if g.state == guardGround && g.remaining == 0 && !needsGuard(data) {
		return data
	}
	out := make([]byte, 0, len(data)+len(g.pending))
	for _, b := range data {
		out = g.step(out, b)
	}
	return out
}

// needsGuard reports whether data could put the guard to work: without an
// escape byte or a C1 string introducer, a ground-state write cannot enter a
// string sequence, so the whole chunk passes through unexamined.
func needsGuard(data []byte) bool {
	for _, b := range data {
		switch b {
		case 0x1B, 0x90, 0x98, 0x9D, 0x9E, 0x9F:
			return true
		}
		// A lead byte can carry one of the introducers above as a
		// continuation byte, which the guard must not read as an introducer.
		if utf8Length(b) > 0 {
			return true
		}
	}
	return false
}

func (g *stringGuard) step(out []byte, b byte) []byte {
	// Finish a character already in flight before looking at the byte again.
	if g.remaining > 0 {
		if b >= 0x80 && b <= 0xBF {
			g.pending = append(g.pending, b)
			g.remaining--
			if g.remaining == 0 {
				return g.flush(out)
			}
			return out
		}
		// Malformed input: hand the incomplete character to the parser and
		// judge this byte on its own.
		out = g.flush(out)
	}

	switch g.state {
	case guardGround:
		switch {
		case b == 0x1B:
			g.state = guardEscape
		case b == 0x90 || b == 0x9D:
			g.state, g.asciiOnly = guardString, false
		case b == 0x98 || b == 0x9E || b == 0x9F:
			g.state, g.asciiOnly = guardString, true
		default:
			// Track characters here too, so a continuation byte that happens
			// to equal a C1 introducer does not look like one.
			if n := utf8Length(b); n > 0 {
				g.pending, g.remaining = append(g.pending[:0], b), n-1
				return out
			}
		}
		return append(out, b)
	case guardEscape:
		switch b {
		case ']', 'P':
			g.state, g.asciiOnly = guardString, false
		case 'X', '^', '_':
			g.state, g.asciiOnly = guardString, true
		case 0x1B:
		default:
			g.state = guardGround
		}
		return append(out, b)
	default:
		switch b {
		case 0x1B:
			g.state = guardEscape
		case 0x07, 0x18, 0x1A, 0x9C:
			g.state = guardGround
		default:
			if n := utf8Length(b); n > 0 {
				g.pending, g.remaining = append(g.pending[:0], b), n-1
				return out
			}
		}
		return append(out, b)
	}
}

// flush emits the buffered character, or drops it when the parser would leave
// the string sequence partway through it.
func (g *stringGuard) flush(out []byte) []byte {
	character, drop := g.pending, false
	g.pending, g.remaining = g.pending[:0], 0
	if g.state == guardString {
		for _, b := range character {
			if b >= 0x80 && (g.asciiOnly || b <= 0x9F) {
				drop = true
				break
			}
		}
	}
	if drop {
		return out
	}
	return append(out, character...)
}
