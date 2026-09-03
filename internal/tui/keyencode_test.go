package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestEncodeKeyPlainText(t *testing.T) {
	got := encodeKey(tea.KeyPressMsg{Text: "a"})
	if string(got) != "a" {
		t.Fatalf("unexpected encoding: %q", got)
	}
}

func TestEncodeKeyUnicodeText(t *testing.T) {
	got := encodeKey(tea.KeyPressMsg{Text: "é"})
	if string(got) != "é" {
		t.Fatalf("unexpected encoding: %q", got)
	}
}

func TestEncodeKeyControlCombos(t *testing.T) {
	cases := []struct {
		name string
		msg  tea.KeyPressMsg
		want string
	}{
		{"ctrl+c", tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}, "\x03"},
		{"ctrl+d", tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl}, "\x04"},
		{"ctrl+a", tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl}, "\x01"},
		{"ctrl+space", tea.KeyPressMsg{Code: ' ', Mod: tea.ModCtrl}, "\x00"},
		{"enter", tea.KeyPressMsg{Code: tea.KeyEnter}, "\r"},
		{"tab", tea.KeyPressMsg{Code: tea.KeyTab}, "\t"},
		{"shift+tab", tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}, "\x1b[Z"},
		{"backspace", tea.KeyPressMsg{Code: tea.KeyBackspace}, "\x7f"},
		{"escape", tea.KeyPressMsg{Code: tea.KeyEscape}, "\x1b"},
		{"up", tea.KeyPressMsg{Code: tea.KeyUp}, "\x1b[A"},
		{"down", tea.KeyPressMsg{Code: tea.KeyDown}, "\x1b[B"},
		{"right", tea.KeyPressMsg{Code: tea.KeyRight}, "\x1b[C"},
		{"left", tea.KeyPressMsg{Code: tea.KeyLeft}, "\x1b[D"},
		{"home", tea.KeyPressMsg{Code: tea.KeyHome}, "\x1b[H"},
		{"end", tea.KeyPressMsg{Code: tea.KeyEnd}, "\x1b[F"},
		{"pgup", tea.KeyPressMsg{Code: tea.KeyPgUp}, "\x1b[5~"},
		{"pgdown", tea.KeyPressMsg{Code: tea.KeyPgDown}, "\x1b[6~"},
		{"insert", tea.KeyPressMsg{Code: tea.KeyInsert}, "\x1b[2~"},
		{"delete", tea.KeyPressMsg{Code: tea.KeyDelete}, "\x1b[3~"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := encodeKey(testCase.msg)
			if string(got) != testCase.want {
				t.Fatalf("encodeKey(%+v) = %q, want %q", testCase.msg, got, testCase.want)
			}
		})
	}
}

func TestEncodeKeyAltPrefixesEscape(t *testing.T) {
	got := encodeKey(tea.KeyPressMsg{Code: tea.KeyBackspace, Mod: tea.ModAlt})
	if string(got) != "\x1b\x7f" {
		t.Fatalf("unexpected encoding: %q", got)
	}

	got = encodeKey(tea.KeyPressMsg{Text: "a", Code: 'a', Mod: tea.ModAlt})
	if string(got) != "\x1ba" {
		t.Fatalf("unexpected encoding: %q", got)
	}
}

func TestEncodeKeyUnhandledReturnsNil(t *testing.T) {
	if got := encodeKey(tea.KeyPressMsg{Code: tea.KeyF1}); got != nil {
		t.Fatalf("expected nil for an unhandled key, got %q", got)
	}
}
