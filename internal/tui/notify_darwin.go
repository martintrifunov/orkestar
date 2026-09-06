package tui

import (
	"os/exec"
	"strings"
)

// resolveNotifier posts through AppleScript, which every macOS has, rather
// than terminal-notifier, which most do not.
func resolveNotifier() notifier {
	path, err := exec.LookPath("osascript")
	if err != nil {
		return notifier{}
	}
	return notifier{program: path, arguments: func(title, body string) []string {
		return []string{"-e", "display notification " + appleScriptString(body) + " with title " + appleScriptString(title)}
	}}
}

// appleScriptString quotes text for AppleScript source. The body carries a
// task title, which is whatever a user or an agent typed, and this is a script
// being compiled rather than an argument being passed: an unescaped quote in a
// task name would end the string and run whatever followed it.
func appleScriptString(text string) string {
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	// Newlines end a statement in AppleScript source, so they cannot survive
	// as themselves either.
	return `"` + replacer.Replace(strings.ReplaceAll(text, "\n", " ")) + `"`
}
