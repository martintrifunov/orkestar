package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/martintrifunov/orkestar/internal/daemon"
	"github.com/martintrifunov/orkestar/internal/usage"
	"github.com/martintrifunov/orkestar/internal/workflow"
)

// Sidebar rows were hard-coded, so a user who cared about the branch rather
// than the status had no way to say so. A row template is a string with
// {token} placeholders, set per section in tui.json; an absent key keeps the
// built-in row, and an unknown token is reported at startup rather than
// rendering as an empty column nobody can explain.

type sidebarTemplates struct {
	Task    string `json:"task,omitempty"`
	Session string `json:"session,omitempty"`
	Agent   string `json:"agent,omitempty"`
}

// sidebarTokens lists what each row kind can interpolate, so validation and
// rendering cannot drift apart.
var sidebarTokens = map[string][]string{
	"task":    {"status", "title", "id", "branch", "worktree", "assignee", "depends", "review"},
	"agent":   {"adapter", "state", "machine", "task", "id", "attention", "terminal", "usage"},
	"session": {"state", "command", "id", "directory"},
}

// sidebarComplaints reports tokens a template names that do not exist, the way
// binding complaints do: a row that silently drops a field is worse than one
// that was refused.
func sidebarComplaints(templates sidebarTemplates) []string {
	var complaints []string
	for _, row := range []struct {
		kind     string
		template string
	}{
		{"task", templates.Task},
		{"agent", templates.Agent},
		{"session", templates.Session},
	} {
		for _, token := range templateTokens(row.template) {
			known := false
			for _, candidate := range sidebarTokens[row.kind] {
				if token == candidate {
					known = true
					break
				}
			}
			if !known {
				complaints = append(complaints, fmt.Sprintf("the %s row has no {%s} token", row.kind, token))
			}
		}
	}
	return complaints
}

// templateTokens extracts the placeholder names from a template.
func templateTokens(template string) []string {
	var tokens []string
	for {
		open := strings.Index(template, "{")
		if open < 0 {
			return tokens
		}
		close := strings.Index(template[open:], "}")
		if close < 0 {
			return tokens
		}
		tokens = append(tokens, strings.TrimSpace(template[open+1:open+close]))
		template = template[open+close+1:]
	}
}

// sidebarRow renders one template. A token with no value renders empty; that
// is what an evidence-less department should look like rather than a literal
// "{branch}" on every row.
func sidebarRow(template string, values map[string]string) string {
	var out strings.Builder
	for {
		open := strings.Index(template, "{")
		if open < 0 {
			out.WriteString(template)
			return out.String()
		}
		close := strings.Index(template[open:], "}")
		if close < 0 {
			out.WriteString(template)
			return out.String()
		}
		out.WriteString(template[:open])
		out.WriteString(values[strings.TrimSpace(template[open+1:open+close])])
		template = template[open+close+1:]
	}
}

// taskRow is one primary task line: the configured template, or the built-in
// status and title.
func (m Model) taskRow(task workflow.Task) string {
	if m.settings.Sidebar.Task == "" {
		return fmt.Sprintf("%-11s  %s", task.Status, task.Title)
	}
	values := map[string]string{
		"status":   string(task.Status),
		"title":    task.Title,
		"id":       task.ID,
		"branch":   task.WorktreeBranch,
		"worktree": task.WorktreePath,
		"assignee": m.agentAdapterOf(task.AssigneeAgentID),
		"depends":  strconv.Itoa(m.blockedBy(task)),
		"review":   "",
	}
	if task.AutoReview {
		values["review"] = "review"
	}
	return sidebarRow(m.settings.Sidebar.Task, values)
}

// agentRow is one agent line. The machine column the built-in row adds when
// several machines are watched belongs to the template in template mode; the
// token is there for anyone who wants it.
func (m Model) agentRow(scoped scopedAgent) string {
	if m.settings.Sidebar.Agent == "" {
		line := scoped.Agent.Adapter + "  " + scoped.Agent.State
		if scoped.Agent.TokensUsed > 0 {
			line += "  " + usage.FormatTokens(scoped.Agent.TokensUsed) + " tok"
		}
		if len(m.machines) > 1 {
			line += "  [" + scoped.MachineLabel + "]"
		}
		return line
	}
	usageText := ""
	if scoped.Agent.TokensUsed > 0 {
		usageText = usage.FormatTokens(scoped.Agent.TokensUsed) + " tok"
	}
	return sidebarRow(m.settings.Sidebar.Agent, map[string]string{
		"adapter":   scoped.Agent.Adapter,
		"state":     scoped.Agent.State,
		"machine":   scoped.MachineLabel,
		"task":      m.taskTitleOfScoped(scoped),
		"id":        scoped.Agent.ID,
		"attention": scoped.Agent.AttentionReason,
		"terminal":  scoped.Agent.TerminalID,
		"usage":     usageText,
	})
}

func (m Model) sessionRow(terminal daemon.Terminal) string {
	if m.settings.Sidebar.Session == "" {
		return fmt.Sprintf("%-9s  %s", terminal.State, strings.Join(terminal.Command, " "))
	}
	return sidebarRow(m.settings.Sidebar.Session, map[string]string{
		"state":     terminal.State,
		"command":   strings.Join(terminal.Command, " "),
		"id":        terminal.ID,
		"directory": terminal.Directory,
	})
}
