// Package hooks builds invocation-local lifecycle configuration. It never edits
// user or project agent settings or changes an agent's approval policy.
package hooks

import (
	"encoding/json"
	"strconv"
)

var Events = []string{"SessionStart", "UserPromptSubmit", "Stop", "PermissionRequest", "SessionEnd"}

func Claude(command string) string {
	hooks := map[string]any{}
	for _, event := range append(append([]string{}, Events...), "Notification") {
		hooks[event] = []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": command, "timeout": 600}}}}
	}
	b, _ := json.Marshal(map[string]any{"hooks": hooks})
	return string(b)
}
func Codex(command string) []string {
	var args []string
	for _, event := range Events {
		timeout := "600"
		if event == "SessionEnd" {
			timeout = "3"
		}
		args = append(args, "-c", "hooks."+event+"=[{hooks=[{type=\"command\",command="+strconv.Quote(command)+",timeout="+timeout+"}]}]")
	}
	return args
}
