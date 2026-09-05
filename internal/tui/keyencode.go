package tui

import (
	"unicode"

	tea "charm.land/bubbletea/v2"
)

// encodeKey turns a key press into the raw bytes a real terminal would have
// sent to a PTY-backed process. It exists because the embedded terminal
// pane bypasses a real TTY entirely: Bubble Tea already decodes raw input
// into structured key events for us, so nothing downstream still produces
// the byte sequences an interactive CLI expects. It covers what driving an
// interactive coding-agent CLI needs — plain text, Enter/Tab/Backspace/Esc,
// Ctrl+letter, arrows, navigation keys, and Alt-prefixing — not the full
// terminal input surface (no function keys; CSI-u carries Shift+Enter).
// An unrecognized key returns nil, which callers should treat as "nothing to
// send" rather than an error.
func encodeKey(msg tea.KeyPressMsg) []byte {
	mod := msg.Mod
	if mod&tea.ModAlt != 0 {
		rest := encodeKey(tea.KeyPressMsg{Text: msg.Text, Code: msg.Code, Mod: mod &^ tea.ModAlt})
		if rest == nil {
			return nil
		}
		return append([]byte{0x1b}, rest...)
	}

	if mod&tea.ModCtrl != 0 {
		if data, ok := ctrlByte(msg.Code); ok {
			return data
		}
	}

	switch msg.Code {
	case tea.KeyEnter:
		if mod&tea.ModShift != 0 {
			return []byte("\x1b[13;2u")
		}
		return []byte{'\r'}
	case tea.KeyTab:
		if mod&tea.ModShift != 0 {
			return []byte("\x1b[Z")
		}
		return []byte{'\t'}
	case tea.KeyBackspace:
		return []byte{0x7f}
	case tea.KeyEscape:
		return []byte{0x1b}
	case tea.KeyUp:
		return []byte("\x1b[A")
	case tea.KeyDown:
		return []byte("\x1b[B")
	case tea.KeyRight:
		return []byte("\x1b[C")
	case tea.KeyLeft:
		return []byte("\x1b[D")
	case tea.KeyHome:
		return []byte("\x1b[H")
	case tea.KeyEnd:
		return []byte("\x1b[F")
	case tea.KeyPgUp:
		return []byte("\x1b[5~")
	case tea.KeyPgDown:
		return []byte("\x1b[6~")
	case tea.KeyInsert:
		return []byte("\x1b[2~")
	case tea.KeyDelete:
		return []byte("\x1b[3~")
	}

	if msg.Text != "" {
		return []byte(msg.Text)
	}
	// Special keys (arrows, function keys, etc.) use rune values above
	// unicode.MaxRune as sentinels; unicode.IsGraphic rejects those, so
	// this only catches genuine printable characters (e.g. a plain 'a'
	// with no Text set, which some input paths produce).
	if mod == 0 && unicode.IsGraphic(msg.Code) {
		return []byte(string(msg.Code))
	}
	return nil
}

// ctrlByte returns the control byte for Ctrl+code, if code is one this
// package knows how to encode.
func ctrlByte(code rune) ([]byte, bool) {
	switch {
	case code >= 'a' && code <= 'z':
		return []byte{byte(code - 'a' + 1)}, true
	case code >= 'A' && code <= 'Z':
		return []byte{byte(code - 'A' + 1)}, true
	case code == ' ':
		return []byte{0x00}, true
	case code == '[':
		return []byte{0x1b}, true
	case code == '\\':
		return []byte{0x1c}, true
	case code == ']':
		return []byte{0x1d}, true
	case code == '^':
		return []byte{0x1e}, true
	case code == '_':
		return []byte{0x1f}, true
	default:
		return nil, false
	}
}
