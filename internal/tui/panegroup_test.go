package tui

import (
	"strings"
	"testing"

	"github.com/martintrifunov/orkestar/internal/daemon"
	"github.com/martintrifunov/orkestar/internal/workflow"
)

// taskModel is a sidebar with two tasks, an agent on each, and panes open for
// them: the situation where panes needed grouping in the first place.
func taskModel(t *testing.T) (Model, *embeddedTerminal, *embeddedTerminal) {
	t.Helper()
	first := &embeddedTerminal{terminalID: "term_1", emulator: &staticScreen{}, done: make(chan struct{})}
	second := &embeddedTerminal{terminalID: "term_2", emulator: &staticScreen{}, done: make(chan struct{})}

	m := Model{width: 160, height: 48, focus: focusTasks}
	// Grown from nothing, the way the model does it: an empty root node has
	// neither a pane nor children and cannot be rendered.
	m.layout = (*splitNode)(nil).insert(nil, first, false)
	m.layout = m.layout.insert(first, second, false)
	m.embedded = first
	m.snapshot.Tasks = []workflow.Task{
		{ID: "task_1", Title: "Ship it", Status: workflow.StatusInProgress},
		{ID: "task_2", Title: "Fix the parser", Status: workflow.StatusInProgress},
	}
	m.snapshot.Agents = []daemon.Agent{
		{ID: "agent_1", Adapter: "claude-code", TaskID: "task_1", TerminalID: "term_1", State: "working"},
		{ID: "agent_2", Adapter: "claude-code", TaskID: "task_2", TerminalID: "term_2", State: "working"},
	}
	m.snapshot.Terminals = []daemon.Terminal{
		{ID: "term_1", Command: []string{"agent:claude-code"}},
		{ID: "term_2", Command: []string{"agent:claude-code"}},
	}
	return m, first, second
}

// Two agent panes running the same CLI are indistinguishable by command. What
// separates them is the work, so the border says which task each is doing.
func TestPaneBordersNameTheirTask(t *testing.T) {
	m, first, second := taskModel(t)

	// The adapter prefix the daemon marks agent terminals with is not part of
	// the label: what a border has room to say is the agent and the work.
	if label := m.paneLabel(first); label != "claude-code · Ship it" {
		t.Fatalf("the first pane is labelled %q", label)
	}
	if label := m.paneLabel(second); !strings.Contains(label, "Fix the parser") {
		t.Fatalf("the second pane is labelled %q", label)
	}
	// A pane doing no task keeps the plain label rather than gaining an empty
	// separator.
	shell := &embeddedTerminal{terminalID: "term_3", emulator: &staticScreen{}, done: make(chan struct{})}
	m.snapshot.Terminals = append(m.snapshot.Terminals, daemon.Terminal{ID: "term_3", Command: []string{"/bin/zsh"}})
	if label := m.paneLabel(shell); label != "zsh" {
		t.Fatalf("a task-free pane is labelled %q", label)
	}
}

// The Tasks list answers "which of this is on screen" without the user
// matching pane borders by eye.
func TestTaskListMarksWhatIsOnScreen(t *testing.T) {
	m, _, _ := taskModel(t)

	rendered := m.renderTasks()
	if strings.Count(rendered, "on screen") != 2 {
		t.Fatalf("both tasks should be marked as open:\n%s", rendered)
	}

	// A task nothing is showing is not marked.
	m.snapshot.Tasks = append(m.snapshot.Tasks, workflow.Task{ID: "task_3", Title: "Not started", Status: workflow.StatusPending})
	if strings.Count(m.renderTasks(), "on screen") != 2 {
		t.Fatalf("a task with no pane was marked as open:\n%s", m.renderTasks())
	}
}

// Enter on a task shows the work being done on it, which is the whole point of
// the grouping: get from the board to the pane without hunting.
func TestEnterOnATaskFocusesItsPane(t *testing.T) {
	m, first, second := taskModel(t)
	m.taskSelected = 1
	m.sidebarFocused = true

	if cmd := m.openSelectedTask(); cmd != nil {
		t.Fatalf("focusing an open pane should not need a command: %v", cmd())
	}
	if m.embedded != second {
		t.Fatal("enter did not move to the second task's pane")
	}
	if m.sidebarFocused {
		t.Fatal("focus stayed in the sidebar")
	}

	m.taskSelected = 0
	m.openSelectedTask()
	if m.embedded != first {
		t.Fatal("enter did not move to the first task's pane")
	}
}

// With more than one pane on a task, enter cycles within that task rather than
// re-focusing the same pane. This is the one place a task's panes act as a
// group.
func TestEnterCyclesWithinATask(t *testing.T) {
	m, first, _ := taskModel(t)
	extra := &embeddedTerminal{terminalID: "term_3", emulator: &staticScreen{}, done: make(chan struct{})}
	m.layout = m.layout.insert(first, extra, true)
	m.snapshot.Agents = append(m.snapshot.Agents, daemon.Agent{
		ID: "agent_3", Adapter: "codex", TaskID: "task_1", TerminalID: "term_3", State: "working",
	})
	m.taskSelected = 0

	group := m.panesForTask("task_1")
	if len(group) != 2 {
		t.Fatalf("the task has %d panes, want 2", len(group))
	}

	m.embedded = group[0]
	m.openSelectedTask()
	if m.embedded != group[1] {
		t.Fatal("enter did not move to the task's other pane")
	}
	m.openSelectedTask()
	if m.embedded != group[0] {
		t.Fatal("enter did not wrap back round the task's panes")
	}
	// And never leaves the task.
	if task, ok := m.taskOfPane(m.embedded); !ok || task.ID != "task_1" {
		t.Fatalf("cycling left the task: %v", m.embedded.terminalID)
	}
}

// A task nobody has started says how to start one rather than doing nothing.
func TestEnterOnAnUnstartedTaskSaysSo(t *testing.T) {
	m, _, _ := taskModel(t)
	m.snapshot.Tasks = append(m.snapshot.Tasks, workflow.Task{ID: "task_3", Title: "Not started"})
	m.taskSelected = 2

	if cmd := m.openSelectedTask(); cmd != nil {
		t.Fatalf("an unstarted task produced %v", cmd())
	}
	if !strings.Contains(m.notice, "start an agent") {
		t.Fatalf("the notice does not say what to do: %q", m.notice)
	}
}

// A task whose agent is running but whose pane was closed reopens it, rather
// than attaching a second pane to a terminal that is already on screen.
func TestFocusOrOpenPrefersAPaneThatExists(t *testing.T) {
	m, first, _ := taskModel(t)
	m.embedded = nil

	if cmd := m.focusOrOpen("term_1"); cmd != nil {
		t.Fatalf("an open terminal should not be opened again: %v", cmd())
	}
	if m.embedded != first {
		t.Fatal("the existing pane was not focused")
	}
	if len(m.visiblePanes()) != 2 {
		t.Fatalf("%d panes exist, want the original 2", len(m.visiblePanes()))
	}

	// One that is not open needs a command to open it.
	m.snapshot.Terminals = append(m.snapshot.Terminals, daemon.Terminal{ID: "term_closed"})
	if cmd := m.focusOrOpen("term_closed"); cmd == nil {
		t.Fatal("a closed terminal was not opened")
	}
}

// The header names the focused pane the same way its border does, rather than
// falling back to a terminal ID nobody can place.
func TestHeaderNamesTheFocusedTask(t *testing.T) {
	m, _, second := taskModel(t)
	m.embedded = second
	if title := m.paneTitle(); !strings.Contains(title, "Fix the parser") {
		t.Fatalf("the header says %q", title)
	}
}
