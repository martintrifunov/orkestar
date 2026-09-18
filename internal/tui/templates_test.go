package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/martintrifunov/orkestar/internal/agent"
	"github.com/martintrifunov/orkestar/internal/workflow"
)

// writeTemplate puts a template where the daemon reads it: inside the
// workspace, beside the code it describes.
func writeTemplate(t *testing.T, root, name, body string) {
	t.Helper()
	directory := filepath.Join(root, ".orkestar", "templates")
	if err := os.MkdirAll(directory, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, name+".json"), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}

// A template could only be applied from the command line; the board it
// creates tasks on could not reach it.
func TestApplyTemplateFromTheBoard(t *testing.T) {
	root := taskRepo(t)
	writeTemplate(t, root, "release", `{
		"name": "release",
		"description": "cut a release",
		"tasks": [
			{"key": "tests", "title": "Run the suite"},
			{"key": "notes", "title": "Write notes", "depends_on": ["tests"]}
		]
	}`)
	client := startEmbeddedTestDaemon(t)
	m := New(client, root)
	m.width, m.height = 160, 44
	m.focus = focusTasks

	m, cmd := press(t, m, 'T')
	if !m.pickingTemplate || cmd == nil {
		t.Fatal("T did not open the template picker and read templates")
	}
	m = settle(t, m, cmd)
	if m.templatesErr != nil || len(m.templates) != 1 || m.templates[0].Name != "release" {
		t.Fatalf("templates not loaded: %v %+v", m.templatesErr, m.templates)
	}
	if view := m.renderTemplatePicker(80); !strings.Contains(view, "release") || !strings.Contains(view, "2 tasks") {
		t.Fatalf("picker does not describe the template:\n%s", view)
	}
	// Start defaults off, matching the CLI: creating tasks is recoverable,
	// while launching every agent a template names spends tokens immediately.
	if m.templateStart {
		t.Fatal("start should default off")
	}
	updated, _ := m.Update(tea.KeyPressMsg{Code: 's'})
	m = updated.(Model)
	if !m.templateStart || !strings.Contains(m.renderTemplatePicker(100), "start agents: on") {
		t.Fatal("s did not toggle start")
	}

	updated, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if m.pickingTemplate || cmd == nil || !m.taskBusy {
		t.Fatal("enter did not apply the template")
	}
	m = settle(t, m, cmd)
	if m.err != nil {
		t.Fatalf("apply failed: %v", m.err)
	}
	if len(m.snapshot.Tasks) != 2 {
		t.Fatalf("template tasks were not created: %+v", m.snapshot.Tasks)
	}
	var first, second workflow.Task
	for _, task := range m.snapshot.Tasks {
		switch task.Title {
		case "Run the suite":
			first = task
		case "Write notes":
			second = task
		}
	}
	if first.ID == "" || second.ID == "" {
		t.Fatalf("template tasks are missing: %+v", m.snapshot.Tasks)
	}
	if len(second.DependsOn) != 1 || second.DependsOn[0] != first.ID {
		t.Fatalf("the template's dependency was not resolved: %+v", second.DependsOn)
	}
	if !strings.Contains(m.notice, "2 tasks") {
		t.Fatalf("apply was not reported: %q", m.notice)
	}
}

// Applying with start on launches the agents the template names, but only for
// tasks nothing is blocking; the rest are reported as waiting.
func TestApplyTemplateStartsUnblockedAgents(t *testing.T) {
	root := taskRepo(t)
	writeTemplate(t, root, "pipeline", `{
		"name": "pipeline",
		"tasks": [
			{"key": "tests", "title": "Run the suite", "agent": "fake"},
			{"key": "notes", "title": "Write notes", "depends_on": ["tests"], "agent": "fake"}
		]
	}`)
	adapter := agent.NewFakeAdapter(agent.Capabilities{
		Name: "fake", SupportsInteractive: true, SupportsPrompt: true,
	})
	client := startEmbeddedTestDaemon(t, adapter)
	m := New(client, root)
	m.width, m.height = 160, 44
	m.focus = focusTasks

	m, cmd := press(t, m, 'T')
	m = settle(t, m, cmd)
	updated, _ := m.Update(tea.KeyPressMsg{Code: 's'})
	m = updated.(Model)
	updated, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	m = settle(t, m, cmd)
	if m.err != nil {
		t.Fatalf("apply with start failed: %v", m.err)
	}
	if len(m.snapshot.Agents) != 1 {
		t.Fatalf("the unblocked task's agent was not started: %+v", m.snapshot.Agents)
	}
	if !strings.Contains(m.notice, "1 agents started") || !strings.Contains(m.notice, "1 waiting") {
		t.Fatalf("apply did not report the split: %q", m.notice)
	}
}

func TestTemplatePickerWithNoneAndEscape(t *testing.T) {
	root := taskRepo(t)
	client := startEmbeddedTestDaemon(t)
	m := New(client, root)
	m.width, m.height = 160, 44
	m.focus = focusTasks

	m, cmd := press(t, m, 'T')
	m = settle(t, m, cmd)
	if len(m.templates) != 0 || m.templatesErr != nil {
		t.Fatalf("unexpected templates: %v %+v", m.templatesErr, m.templates)
	}
	if view := m.renderTemplatePicker(80); !strings.Contains(view, "No templates") {
		t.Fatalf("empty picker does not say so:\n%s", view)
	}

	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(Model)
	if !m.pickingTemplate {
		t.Fatal("enter with no templates should leave the picker open")
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(Model)
	if m.pickingTemplate {
		t.Fatal("esc did not close the picker")
	}
}
