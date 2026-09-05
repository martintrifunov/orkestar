package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/martintrifunov/orkestar/internal/daemon"
)

// Nothing could stop a session or clear a finished one, so the list grew
// without bound and a hung process could only be ended by shutting the daemon
// down. This drives the whole path against a real daemon.
func TestStopAndRemoveASessionFromTheSidebar(t *testing.T) {
	client := startEmbeddedTestDaemon(t)
	m := New(client, t.TempDir())
	m.width, m.height = 160, 44
	m.focus = focusSessions

	started := startEmbeddedTestTerminal(t, client, []string{"/bin/sh", "-c", "printf 'ready\\n'; while :; do sleep 1; done"})
	ready := openEmbeddedTerminal(client, started.ID, 80, 24)().(embeddedReadyMsg)
	if ready.err != nil {
		t.Fatal(ready.err)
	}
	m.addPane(ready.terminal)
	waitForEmbeddedEvent(t, ready.terminal, "ready")
	// Sidebar focus, as Ctrl+b Tab gives; otherwise keys reach the terminal.
	m.sidebarFocused = true
	m = settle(t, m, m.loadSnapshot())
	if len(m.snapshot.Terminals) != 1 || m.snapshot.Terminals[0].State != "running" {
		t.Fatalf("session is not running: %+v", m.snapshot.Terminals)
	}

	// Stopping something live asks first, because it ends work the daemon
	// holds independently of this UI.
	m, cmd := press(t, m, 'X')
	if cmd != nil {
		t.Fatal("the first press stopped a running session without asking")
	}
	if !strings.Contains(m.notice, "Press X again") {
		t.Fatalf("no confirmation was offered: %q", m.notice)
	}
	m, cmd = press(t, m, 'X')
	if cmd == nil {
		t.Fatal("the second press did not stop the session")
	}
	m = settle(t, m, cmd)
	if m.err != nil {
		t.Fatalf("stop failed: %v", m.err)
	}
	if state := m.snapshot.Terminals[0].State; state == "running" {
		t.Fatalf("the process is still running: %s", state)
	}
	if !strings.Contains(m.notice, "Stopped") {
		t.Fatalf("stop was not reported: %q", m.notice)
	}

	// A finished session is cleared immediately, and its pane goes with it.
	m, cmd = press(t, m, 'X')
	if cmd == nil {
		t.Fatal("a finished session was not removed")
	}
	m = settle(t, m, cmd)
	if m.err != nil {
		t.Fatalf("remove failed: %v", m.err)
	}
	if len(m.snapshot.Terminals) != 0 {
		t.Fatalf("the session is still listed: %+v", m.snapshot.Terminals)
	}
	if len(m.visiblePanes()) != 0 {
		t.Fatal("the pane attached to the removed session stayed open")
	}
}

func TestRemovingARunningSessionIsRefusedByTheDaemon(t *testing.T) {
	client := startEmbeddedTestDaemon(t)
	started := startEmbeddedTestTerminal(t, client, []string{"/bin/sh", "-c", "while :; do sleep 1; done"})
	m := New(client, t.TempDir())
	m.width, m.height = 160, 44
	m = settle(t, m, m.loadSnapshot())

	// Ask for removal directly, the way a second client racing the UI would.
	cmd := m.lifecycleCall("terminal.remove", map[string]any{"terminal_id": started.ID}, "gone", started.ID)
	msg, ok := cmd().(lifecycleMsg)
	if !ok || msg.err == nil {
		t.Fatal("the daemon removed a running session")
	}
	if !strings.Contains(msg.err.Error(), "stop it first") {
		t.Fatalf("unhelpful refusal: %v", msg.err)
	}
	updated := m
	updated.applyLifecycle(msg)
	if updated.err == nil || updated.notice != "" {
		t.Fatal("the refusal was not surfaced")
	}
	m = settle(t, m, m.loadSnapshot())
	if len(m.snapshot.Terminals) != 1 {
		t.Fatal("the session disappeared anyway")
	}
	if state := m.snapshot.Terminals[0].State; state != "running" {
		t.Fatalf("the refused removal disturbed the session: %s", state)
	}
	cmd = m.lifecycleCall("terminal.stop", map[string]any{"terminal_id": started.ID}, "stopped", "")
	if msg := cmd().(lifecycleMsg); msg.err != nil {
		t.Fatalf("stop failed: %v", msg.err)
	}
}

func TestConfirmationIsPerTargetAndInterruptNeedsAnAgent(t *testing.T) {
	m := Model{width: 160, height: 44, focus: focusSessions}
	m.snapshot.Terminals = []daemon.Terminal{
		{ID: "term_1", State: "running", Command: []string{"sh"}, CreatedAt: time.Now()},
		{ID: "term_2", State: "running", Command: []string{"zsh"}, CreatedAt: time.Now()},
	}
	m, cmd := press(t, m, 'X')
	if cmd != nil || m.pendingStop != "term_1" {
		t.Fatal("the first session was not armed")
	}
	// Moving to another session must not carry the confirmation with it.
	m.selected = 1
	m, cmd = press(t, m, 'X')
	if cmd != nil {
		t.Fatal("a confirmation armed on one session stopped another")
	}
	if m.pendingStop != "term_2" || !strings.Contains(m.notice, "zsh") {
		t.Fatalf("the confirmation did not move with the selection: %q", m.notice)
	}

	// Interrupt only applies to a running agent.
	m.focus = focusAgents
	if _, cmd := press(t, m, 'i'); cmd != nil {
		t.Fatal("interrupt acted with no agent selected")
	}
	m.snapshot.Agents = []daemon.Agent{{ID: "agent_1", Adapter: "claude-code", State: "stopped"}}
	m, cmd = press(t, m, 'i')
	if cmd != nil || !strings.Contains(m.notice, "not running") {
		t.Fatalf("interrupting a stopped agent: cmd=%v notice=%q", cmd != nil, m.notice)
	}
	m.snapshot.Agents[0].State = "working"
	if _, cmd := press(t, m, 'i'); cmd == nil {
		t.Fatal("a running agent could not be interrupted")
	}
	// A stopped agent is removed rather than stopped, with no confirmation.
	m.snapshot.Agents[0].State = "stopped"
	m, cmd = press(t, m, 'X')
	if cmd == nil || m.pendingStop != "" {
		t.Fatal("clearing a finished agent should be immediate")
	}
}
