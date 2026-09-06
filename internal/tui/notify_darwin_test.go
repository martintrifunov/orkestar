package tui

import (
	"strings"
	"testing"
)

// The notification body carries a task title, which is whatever a person or an
// agent typed, and on macOS it is compiled as AppleScript source rather than
// passed as an argument. An unescaped quote would close the string and run
// whatever came after it.
func TestAppleScriptStringIsNotEscapable(t *testing.T) {
	for _, test := range []struct{ name, text, want string }{
		{"plain", `Ship it`, `"Ship it"`},
		{"quote", `Ship "it"`, `"Ship \"it\""`},
		{"backslash", `a\b`, `"a\\b"`},
		{"newline becomes a space", "first\nsecond", `"first second"`},
		{
			"a title written to break out",
			`x" with title "y" -- do shell script "touch /tmp/pwned`,
			`"x\" with title \"y\" -- do shell script \"touch /tmp/pwned"`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := appleScriptString(test.text)
			if got != test.want {
				t.Fatalf("got %s, want %s", got, test.want)
			}
			// Whatever the input, the result is one quoted string: the only
			// bare quotes are the first and last.
			if inner := got[1 : len(got)-1]; strings.Count(inner, `"`) != strings.Count(inner, `\"`) {
				t.Fatalf("an unescaped quote survived: %s", got)
			}
			if strings.Contains(got, "\n") {
				t.Fatalf("a newline survived: %q", got)
			}
		})
	}
}

// The escaping only matters if it is actually what gets run.
func TestDarwinNotifierEscapesItsArguments(t *testing.T) {
	n := resolveNotifier()
	if n.program == "" {
		t.Skip("osascript is not installed")
	}
	arguments := n.arguments("Orkestar", `Task done: Ship "it"`)
	joined := strings.Join(arguments, " ")
	if !strings.Contains(joined, `\"it\"`) {
		t.Fatalf("the body was not escaped: %v", arguments)
	}
}
