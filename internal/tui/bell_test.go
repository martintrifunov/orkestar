package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/martintrifunov/orkestar/internal/daemon"
	"github.com/martintrifunov/orkestar/internal/workflow"
)

func snapshotOf(tasks []workflow.Task, agents []daemon.Agent) daemon.Snapshot {
	return daemon.Snapshot{Tasks: tasks, Agents: agents}
}

func task(id, title string, status workflow.Status) workflow.Task {
	return workflow.Task{ID: id, Title: title, Status: status}
}

func agentOn(id, adapter, taskID, state string) daemon.Agent {
	return daemon.Agent{ID: id, Adapter: adapter, TaskID: taskID, State: state}
}

func TestBellRingsForFinishedWork(t *testing.T) {
	for _, test := range []struct {
		name              string
		previous, current daemon.Snapshot
		want              string
	}{
		{
			name:     "a task finishes",
			previous: snapshotOf([]workflow.Task{task("t1", "Ship it", workflow.StatusInProgress)}, nil),
			current:  snapshotOf([]workflow.Task{task("t1", "Ship it", workflow.StatusDone)}, nil),
			want:     "Task done: Ship it",
		},
		{
			name: "an agent stops with its task unfinished",
			previous: snapshotOf(
				[]workflow.Task{task("t1", "Ship it", workflow.StatusInProgress)},
				[]daemon.Agent{agentOn("a1", "claude-code", "t1", "working")}),
			current: snapshotOf(
				[]workflow.Task{task("t1", "Ship it", workflow.StatusInProgress)},
				[]daemon.Agent{agentOn("a1", "claude-code", "t1", "crashed")}),
			want: "claude-code stopped with Ship it unfinished",
		},
		{
			name: "an agent is interrupted by a daemon restart",
			previous: snapshotOf(
				[]workflow.Task{task("t1", "Ship it", workflow.StatusInProgress)},
				[]daemon.Agent{agentOn("a1", "codex", "t1", "ready")}),
			current: snapshotOf(
				[]workflow.Task{task("t1", "Ship it", workflow.StatusInProgress)},
				[]daemon.Agent{agentOn("a1", "codex", "t1", "interrupted")}),
			want: "codex stopped with Ship it unfinished",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := bellFor(test.previous, test.current); got != test.want {
				t.Fatalf("got %q, want %q", got, test.want)
			}
		})
	}
}

// The bell is only worth anything if it stays quiet the rest of the time.
func TestBellStaysQuiet(t *testing.T) {
	working := snapshotOf(
		[]workflow.Task{task("t1", "Ship it", workflow.StatusInProgress)},
		[]daemon.Agent{agentOn("a1", "claude-code", "t1", "working")})

	for _, test := range []struct {
		name              string
		previous, current daemon.Snapshot
	}{
		{"nothing changed", working, working},
		{
			"a task starts",
			snapshotOf([]workflow.Task{task("t1", "Ship it", workflow.StatusPending)}, nil),
			snapshotOf([]workflow.Task{task("t1", "Ship it", workflow.StatusInProgress)}, nil),
		},
		{
			"a task is cancelled",
			snapshotOf([]workflow.Task{task("t1", "Ship it", workflow.StatusInProgress)}, nil),
			snapshotOf([]workflow.Task{task("t1", "Ship it", workflow.StatusCancelled)}, nil),
		},
		{
			"a task that was already done is still done",
			snapshotOf([]workflow.Task{task("t1", "Ship it", workflow.StatusDone)}, nil),
			snapshotOf([]workflow.Task{task("t1", "Ship it", workflow.StatusDone)}, nil),
		},
		{
			"an agent with no task stops",
			snapshotOf(nil, []daemon.Agent{agentOn("a1", "claude-code", "", "working")}),
			snapshotOf(nil, []daemon.Agent{agentOn("a1", "claude-code", "", "stopped")}),
		},
		{
			"an agent stops after finishing its task",
			snapshotOf(
				[]workflow.Task{task("t1", "Ship it", workflow.StatusDone)},
				[]daemon.Agent{agentOn("a1", "claude-code", "t1", "working")}),
			snapshotOf(
				[]workflow.Task{task("t1", "Ship it", workflow.StatusDone)},
				[]daemon.Agent{agentOn("a1", "claude-code", "t1", "stopped")}),
		},
		{
			"an agent stops after its task was cancelled",
			snapshotOf(
				[]workflow.Task{task("t1", "Ship it", workflow.StatusCancelled)},
				[]daemon.Agent{agentOn("a1", "claude-code", "t1", "working")}),
			snapshotOf(
				[]workflow.Task{task("t1", "Ship it", workflow.StatusCancelled)},
				[]daemon.Agent{agentOn("a1", "claude-code", "t1", "stopped")}),
		},
		{
			"an agent that was already stopped is still stopped",
			snapshotOf(
				[]workflow.Task{task("t1", "Ship it", workflow.StatusInProgress)},
				[]daemon.Agent{agentOn("a1", "claude-code", "t1", "stopped")}),
			snapshotOf(
				[]workflow.Task{task("t1", "Ship it", workflow.StatusInProgress)},
				[]daemon.Agent{agentOn("a1", "claude-code", "t1", "stopped")}),
		},
		{
			"an agent appears already stopped",
			snapshotOf([]workflow.Task{task("t1", "Ship it", workflow.StatusInProgress)}, nil),
			snapshotOf(
				[]workflow.Task{task("t1", "Ship it", workflow.StatusInProgress)},
				[]daemon.Agent{agentOn("a1", "claude-code", "t1", "stopped")}),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := bellFor(test.previous, test.current); got != "" {
				t.Fatalf("rang for %q", got)
			}
		})
	}
}

// bellIn digs the BEL out of whatever announce produced. It is a batch now
// that a notification may travel beside the bell, and a batch does not say
// what it holds without running it.
func bellIn(cmd tea.Cmd) (string, bool) {
	if cmd == nil {
		return "", false
	}
	switch message := cmd().(type) {
	case tea.RawMsg:
		text, ok := message.Msg.(string)
		return text, ok
	case tea.BatchMsg:
		for _, inner := range message {
			if text, ok := bellIn(inner); ok {
				return text, true
			}
		}
	}
	return "", false
}

func TestBellCanBeTurnedOff(t *testing.T) {
	var m Model
	if cmd := m.ring(); cmd == nil {
		t.Fatal("the bell is off by default")
	} else if _, ok := cmd().(tea.RawMsg); !ok {
		t.Fatalf("ring produced %T, want a raw write", cmd())
	}

	off := false
	m.settings.Bell = &off
	if cmd := m.ring(); cmd != nil {
		t.Fatal("the bell rang while turned off")
	}
}

// The opening snapshot is not a set of changes: a task that was finished
// yesterday must not ring on attach.
func TestTheFirstSnapshotNeverRings(t *testing.T) {
	m := Model{width: 140, height: 40}
	done := snapshotOf([]workflow.Task{task("t1", "Ship it", workflow.StatusDone)}, nil)

	updated, cmd := m.Update(snapshotMsg{snapshot: done})
	m = updated.(Model)
	if cmd != nil {
		t.Fatalf("the first snapshot produced %v", cmd())
	}
	if m.notice != "" {
		t.Fatalf("the first snapshot set the notice to %q", m.notice)
	}
	if !m.snapshotLoaded {
		t.Fatal("the first snapshot was not recorded")
	}
}

// The whole path, as the model runs it: a later snapshot showing the change
// both rings and says what happened.
func TestASnapshotThatFinishesATaskRings(t *testing.T) {
	m := Model{width: 140, height: 40}
	updated, _ := m.Update(snapshotMsg{snapshot: snapshotOf([]workflow.Task{task("t1", "Ship it", workflow.StatusInProgress)}, nil)})
	m = updated.(Model)

	updated, cmd := m.Update(snapshotMsg{snapshot: snapshotOf([]workflow.Task{task("t1", "Ship it", workflow.StatusDone)}, nil)})
	m = updated.(Model)
	if cmd == nil {
		t.Fatal("a finished task did not ring")
	}
	if text, ok := bellIn(cmd); !ok || !strings.Contains(text, "\a") {
		t.Fatalf("no BEL was written: %q", text)
	}
	if !strings.Contains(m.notice, "Ship it") {
		t.Fatalf("the notice does not name the task: %q", m.notice)
	}
}

// The bell is for someone present; a notification is for someone who is not.
// Posting one while the user is reading the pane it is about is noise.
func TestNotificationsOnlyWhenUnfocused(t *testing.T) {
	if !notificationsSupported() {
		t.Skip("this system cannot post notifications")
	}
	off := false
	for _, test := range []struct {
		name  string
		model Model
		want  bool
	}{
		{"looking at it", Model{focused: true}, false},
		{"looking elsewhere", Model{focused: false}, true},
		{"turned off", Model{focused: false, settings: editorSettings{Notifications: &off}}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := test.model.shouldNotify(); got != test.want {
				t.Fatalf("shouldNotify = %v, want %v", got, test.want)
			}
		})
	}

	// The bell is unconditional either way.
	if (Model{focused: true}).ring() == nil {
		t.Fatal("the bell did not ring for a focused terminal")
	}
	if (Model{focused: false}).ring() == nil {
		t.Fatal("the bell did not ring for an unfocused terminal")
	}
}

// A model that never hears about focus must behave as if the user is present,
// or every notification fires while they are looking at the screen.
func TestFocusDefaultsToPresent(t *testing.T) {
	m := New(nil, t.TempDir())
	if !m.focused {
		t.Fatal("a new model starts unfocused")
	}

	updated, _ := m.Update(tea.BlurMsg{})
	if updated.(Model).focused {
		t.Fatal("blur did not register")
	}
	updated, _ = updated.(Model).Update(tea.FocusMsg{})
	if !updated.(Model).focused {
		t.Fatal("focus did not register")
	}
}

// Turning notifications off must stop them without touching the bell.
func TestNotificationsCanBeTurnedOffSeparatelyFromTheBell(t *testing.T) {
	off := false
	m := Model{settings: editorSettings{Notifications: &off}}
	if !m.settings.bellEnabled() {
		t.Fatal("turning notifications off turned the bell off too")
	}
	if m.settings.notificationsEnabled() {
		t.Fatal("notifications are still on")
	}
}
