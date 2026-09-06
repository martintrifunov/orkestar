package tui

import (
	"fmt"
	"sort"
	"strings"
)

// Every binding used to be a literal in a switch, which meant a user arriving
// from tmux, zellij or vim had to learn Orkestar's fingers rather than the
// other way round. That is the first thing a new person hits and the least
// negotiable: a tool that cannot be rebound is a tool they have to think about
// while using it.
//
// Keys are matched to an action and the code switches on the action, so a
// rebinding changes one map rather than thirty case statements. The help lines
// read from the same map, because a help line that names a key the user has
// changed is worse than none.

// Action is something the interface can be asked to do. The names are stable:
// they appear in tui.json and a rename would silently drop someone's binding.
type Action string

const (
	ActionPrefix     Action = "prefix"
	ActionQuit       Action = "quit"
	ActionNewAgent   Action = "new-agent"
	ActionNewShell   Action = "new-shell"
	ActionOpen       Action = "open"
	ActionRefresh    Action = "refresh"
	ActionFiles      Action = "files"
	ActionSection    Action = "next-section"
	ActionStopRemove Action = "stop-or-remove"
	ActionInterrupt  Action = "interrupt"
	ActionResume     Action = "resume"
	ActionSettings   Action = "settings"
	ActionNewTask    Action = "new-task"
	ActionEditTask   Action = "edit-task"
	ActionDiff       Action = "diff"
	ActionTaskDone   Action = "task-done"
	// ActionDenyOrCancel is one key with two meanings, decided by what has
	// focus: it denies a pending permission, and on a task it cancels it.
	// They were always one binding, so naming them separately would let a user
	// rebind one and be surprised the other moved with it.
	ActionDenyOrCancel Action = "deny-or-cancel"
	ActionWorktree     Action = "worktree"
	ActionAssign       Action = "assign"
	ActionAllow        Action = "permission-allow"
	ActionSplitRight   Action = "split-right"
	ActionSplitDown    Action = "split-down"
	ActionNextPane     Action = "next-pane"
	ActionZoom         Action = "zoom"
	ActionClosePane    Action = "close-pane"
	ActionScrollback   Action = "scrollback"
	ActionEditFile     Action = "edit-file"
	ActionClaimPane    Action = "claim-pane"
	ActionRenamePane   Action = "rename-pane"
	ActionDiscardEdit  Action = "discard-editor"
	ActionDetachPanel  Action = "detach-panel"
)

// defaultBindings is what Orkestar has always used. Anything a user does not
// rebind keeps working exactly as before.
func defaultBindings() map[Action]string {
	return map[Action]string{
		ActionPrefix:       "ctrl+b",
		ActionQuit:         "q",
		ActionNewAgent:     "a",
		ActionNewShell:     "n",
		ActionOpen:         "enter",
		ActionRefresh:      "r",
		ActionFiles:        "f",
		ActionSection:      "tab",
		ActionStopRemove:   "X",
		ActionInterrupt:    "i",
		ActionResume:       "u",
		ActionSettings:     ",",
		ActionNewTask:      "c",
		ActionEditTask:     "e",
		ActionDiff:         "d",
		ActionTaskDone:     "m",
		ActionDenyOrCancel: "x",
		ActionWorktree:     "w",
		ActionAssign:       "t",
		ActionAllow:        "y",
		ActionSplitRight:   "v",
		ActionSplitDown:    "s",
		ActionNextPane:     "o",
		ActionZoom:         "z",
		ActionClosePane:    "q",
		ActionScrollback:   "[",
		ActionEditFile:     "e",
		ActionClaimPane:    "t",
		ActionRenamePane:   "r",
		ActionDiscardEdit:  "x",
		ActionDetachPanel:  "tab",
	}
}

// bindings resolves a key to an action, and an action back to the key that
// invokes it. The two directions are kept together so the help lines cannot
// drift from what the keyboard actually does.
type bindings struct {
	// prefixed are the actions reached after the prefix key; direct are the
	// rest. The same letter means different things in each, which is the whole
	// point of a prefix, so they cannot share one lookup.
	direct   map[string]Action
	prefixed map[string]Action
	keys     map[Action]string
}

// where an action can be invoked. Several are reachable both ways — n opens a
// shell from the sidebar and so does the prefix — while others share a letter
// with a different action on the other side of the prefix, which is what a
// prefix is for. Getting this wrong means either a key that does nothing or
// two actions quietly fighting over one, so it is written out rather than
// inferred.
type placement int

const (
	direct placement = iota
	prefixed
	both
)

var placements = map[Action]placement{
	ActionQuit:         direct,
	ActionOpen:         direct,
	ActionRefresh:      direct,
	ActionSection:      direct,
	ActionStopRemove:   direct,
	ActionInterrupt:    direct,
	ActionResume:       direct,
	ActionNewTask:      direct,
	ActionEditTask:     direct,
	ActionTaskDone:     direct,
	ActionDenyOrCancel: direct,
	ActionWorktree:     direct,
	ActionAssign:       direct,
	ActionAllow:        direct,

	ActionSplitRight:  prefixed,
	ActionSplitDown:   prefixed,
	ActionZoom:        prefixed,
	ActionClosePane:   prefixed,
	ActionScrollback:  prefixed,
	ActionEditFile:    prefixed,
	ActionClaimPane:   prefixed,
	ActionDetachPanel: prefixed,
	ActionSettings:    prefixed,
	ActionDiscardEdit: prefixed,
	ActionRenamePane:  prefixed,

	ActionNewAgent: both,
	ActionNewShell: both,
	ActionFiles:    both,
	ActionDiff:     both,
	ActionNextPane: both,
}

// newBindings merges a user's overrides over the defaults, reporting anything
// it could not use rather than silently ignoring it: a binding that does
// nothing and says nothing is worse than one that was rejected.
func newBindings(overrides map[string]string) (bindings, []string) {
	keys := defaultBindings()
	var complaints []string

	names := make([]string, 0, len(overrides))
	for name := range overrides {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		action := Action(name)
		if _, known := keys[action]; !known {
			complaints = append(complaints, fmt.Sprintf("no action called %q", name))
			continue
		}
		key := strings.TrimSpace(overrides[name])
		if key == "" {
			complaints = append(complaints, fmt.Sprintf("%s has no key", name))
			continue
		}
		keys[action] = key
	}

	resolved := bindings{
		direct:   make(map[string]Action, len(keys)),
		prefixed: make(map[string]Action, len(keys)),
		keys:     keys,
	}
	// Sorted, so which action wins a clash is the same on every run rather
	// than whatever order the map happened to produce.
	actions := make([]Action, 0, len(keys))
	for action := range keys {
		if action != ActionPrefix {
			actions = append(actions, action)
		}
	}
	sort.Slice(actions, func(i, j int) bool { return actions[i] < actions[j] })

	claim := func(table map[string]Action, key string, action Action) {
		if existing, taken := table[key]; taken {
			// The first to claim it keeps it, and the user is told rather than
			// left wondering why one of two keys stopped working.
			complaints = append(complaints, fmt.Sprintf("%q is bound to both %s and %s; %s wins", key, existing, action, existing))
			return
		}
		table[key] = action
	}
	for _, action := range actions {
		key := keys[action]
		switch placements[action] {
		case prefixed:
			claim(resolved.prefixed, key, action)
		case both:
			claim(resolved.direct, key, action)
			claim(resolved.prefixed, key, action)
		default:
			claim(resolved.direct, key, action)
		}
	}
	return resolved, complaints
}

// fallback is what an unset bindings resolves to. A Model built without going
// through New — a test, or any future path that assembles one directly — would
// otherwise have no bindings at all and answer every key with nothing, which
// looks like the interface ignoring the keyboard rather than like a mistake.
var fallback = func() bindings {
	resolved, _ := newBindings(nil)
	return resolved
}()

func (b bindings) resolved() bindings {
	if b.keys == nil {
		return fallback
	}
	return b
}

// action returns what a key does outside the prefix, or an empty action.
func (b bindings) action(key string) Action { return b.resolved().direct[key] }

// prefixAction returns what a key does after the prefix.
func (b bindings) prefixAction(key string) Action { return b.resolved().prefixed[key] }

// key is what invokes an action, for help text that has to stay true.
func (b bindings) key(action Action) string { return b.resolved().keys[action] }

// is reports whether a key invokes an action directly, for the handful of
// places that check one binding rather than dispatching on all of them.
func (b bindings) is(key string, action Action) bool { return b.resolved().direct[key] == action }

// helpLine describes what the keyboard does right now, built from the same
// map that dispatches it. Hard-coded help was true only until somebody
// rebound something, and then it was worse than nothing.
func (m Model) helpLine() string {
	k := m.keys.key
	switch {
	case m.viewingDiff:
		return "esc close diff"
	case m.prompting():
		return "Enter confirm · Esc cancel"
	case m.filesFocused:
		return "↑/↓ select · enter open · ←/→ collapse/expand · " + k(ActionRefresh) + " refresh · esc back"
	case m.prefix:
		return "Prefix: " + k(ActionSplitRight) + "/" + k(ActionSplitDown) + " split · " +
			k(ActionNextPane) + " next · " + k(ActionZoom) + " zoom · arrows resize (repeat) · " +
			k(ActionDiff) + " diff · " + k(ActionEditFile) + " edit · " + k(ActionRenamePane) +
			" rename · " + k(ActionFiles) + " files · esc done"
	case len(m.paneRects()) < len(m.visiblePanes()):
		return "Enlarge window for splits · F6 cycles hidden panes"
	case m.viewingHistory:
		return "Scrollback · pgup/pgdown or wheel · esc return"
	case m.pickingAgent:
		return "up/down select · enter launch · esc cancel"
	case m.focus == focusAgents && (m.embedded == nil || m.sidebarFocused):
		return k(ActionOpen) + " open  " + k(ActionResume) + " resume  " + k(ActionInterrupt) +
			" interrupt  " + k(ActionStopRemove) + " stop/remove  " + k(ActionAllow) + "/" +
			k(ActionDenyOrCancel) + " allow/deny  " + k(ActionSection) + " section"
	case m.focus == focusTasks && (m.embedded == nil || m.sidebarFocused):
		return k(ActionOpen) + " show  " + k(ActionNewTask) + " new  " + k(ActionEditTask) +
			" edit  " + k(ActionNewAgent) + " start  " + k(ActionDiff) + " diff  " +
			k(ActionTaskDone) + " done  " + k(ActionDenyOrCancel) + " cancel  " + k(ActionWorktree) + " worktree"
	case m.embedded != nil && m.sidebarFocused:
		return k(ActionOpen) + " open  " + k(ActionStopRemove) + " stop/remove  " +
			k(ActionNewAgent) + " agent  " + k(ActionNewShell) + " shell  " + k(ActionFiles) +
			" files  esc terminal  " + k(ActionQuit) + " quit"
	case m.embedded != nil:
		return "drag to copy · " + k(ActionPrefix) + " then: " + k(ActionSplitRight) + "/" +
			k(ActionSplitDown) + " split · " + k(ActionNextPane) + " next · " + k(ActionZoom) +
			" zoom · arrows resize · " + k(ActionDiff) + " diff · " + k(ActionEditFile) +
			" edit · " + k(ActionClosePane) + " close"
	}
	return k(ActionNewAgent) + " agent  " + k(ActionNewShell) + " shell  " + k(ActionOpen) +
		" open  " + k(ActionStopRemove) + " stop/remove  " + k(ActionFiles) + " files  " +
		k(ActionSection) + " section  " + k(ActionQuit) + " quit"
}

// prefixBytes is what the prefix key sends to a pane when it is passed
// through. Encoding the bound key rather than a constant is what lets someone
// on ctrl+a reach a nested tmux; sending ctrl+b would leave them no way in.
func (m Model) prefixBytes() []byte {
	key := m.keys.key(ActionPrefix)
	if len(key) > len("ctrl+") && strings.HasPrefix(key, "ctrl+") {
		if letter := key[len("ctrl+"):]; len(letter) == 1 && letter[0] >= 'a' && letter[0] <= 'z' {
			return []byte{letter[0] - 'a' + 1}
		}
	}
	// Anything else is sent as its own text, which is the best that can be
	// done for a prefix that is not a control character.
	return []byte(key)
}
