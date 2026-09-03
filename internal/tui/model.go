package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/martintrifunov/orkestar/internal/daemon"
	"github.com/martintrifunov/orkestar/internal/ipc"
)

var (
	accentStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#D7A84B")).Bold(true)
	dimStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("#777777"))
	selectedStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#111111")).Background(lipgloss.Color("#D7A84B")).Bold(true)
	panelStyle    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color("#5F5F5F")).Padding(0, 1)
	errorStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#FF6B6B"))
)

type snapshotMsg struct {
	snapshot daemon.Snapshot
	err      error
}

type terminalStartedMsg struct {
	terminal daemon.Terminal
	err      error
}

type attachFinishedMsg struct {
	err error
}

type diffMsg struct {
	diff daemon.TaskDiff
	err  error
}

type tickMsg time.Time

// focusPanel is which panel arrow keys move the selection cursor in.
type focusPanel int

const (
	focusSessions focusPanel = iota
	focusTasks
)

type Model struct {
	client       *ipc.Client
	directory    string
	executable   string
	snapshot     daemon.Snapshot
	selected     int
	taskSelected int
	focus        focusPanel
	width        int
	height       int
	loading      bool
	err          error

	viewingDiff bool
	diff        daemon.TaskDiff
	diffErr     error
}

func New(client *ipc.Client, directory, executable string) Model {
	return Model{
		client:     client,
		directory:  directory,
		executable: executable,
		loading:    true,
	}
}

func Run(client *ipc.Client, directory, executable string) error {
	program := tea.NewProgram(New(client, directory, executable))
	_, err := program.Run()
	return err
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.loadSnapshot(), tick())
}

func (m Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch message := message.(type) {
	case tea.WindowSizeMsg:
		m.width = message.Width
		m.height = message.Height
	case tea.KeyPressMsg:
		if m.viewingDiff {
			switch message.String() {
			case "esc", "d", "q":
				m.viewingDiff = false
			}
			return m, nil
		}
		switch message.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "tab":
			if m.focus == focusSessions {
				m.focus = focusTasks
			} else {
				m.focus = focusSessions
			}
		case "up", "k":
			if m.focus == focusTasks {
				if m.taskSelected > 0 {
					m.taskSelected--
				}
			} else if m.selected > 0 {
				m.selected--
			}
		case "down", "j":
			if m.focus == focusTasks {
				if m.taskSelected+1 < len(m.snapshot.Tasks) {
					m.taskSelected++
				}
			} else if m.selected+1 < len(m.snapshot.Terminals) {
				m.selected++
			}
		case "r":
			m.loading = true
			return m, m.loadSnapshot()
		case "n":
			return m, m.startTerminal([]string{defaultShell()})
		case "c":
			return m, m.startTerminal([]string{"claude"})
		case "o":
			return m, m.startTerminal([]string{"opencode"})
		case "enter":
			return m, m.attachSelected()
		case "d":
			return m, m.loadDiff()
		}
	case snapshotMsg:
		m.loading = false
		m.err = message.err
		if message.err == nil {
			m.snapshot = message.snapshot
			if m.selected >= len(m.snapshot.Terminals) && m.selected > 0 {
				m.selected = len(m.snapshot.Terminals) - 1
			}
			if m.taskSelected >= len(m.snapshot.Tasks) && m.taskSelected > 0 {
				m.taskSelected = len(m.snapshot.Tasks) - 1
			}
		}
	case terminalStartedMsg:
		m.loading = false
		m.err = message.err
		if message.err == nil {
			m.snapshot.Terminals = append(m.snapshot.Terminals, message.terminal)
			m.selected = len(m.snapshot.Terminals) - 1
			return m, m.attachSelected()
		}
	case attachFinishedMsg:
		m.err = message.err
		m.loading = true
		return m, m.loadSnapshot()
	case tickMsg:
		return m, tea.Batch(m.loadSnapshot(), tick())
	case diffMsg:
		m.err = message.err
		m.diffErr = message.err
		if message.err == nil {
			m.diff = message.diff
			m.viewingDiff = true
		}
	}
	return m, nil
}

func (m Model) View() tea.View {
	content := m.render()
	view := tea.NewView(content)
	view.AltScreen = true
	view.WindowTitle = "Orkestar"
	return view
}

func (m Model) render() string {
	width := m.width
	if width < 40 {
		width = 40
	}
	if m.viewingDiff {
		return m.renderDiff(width)
	}

	header := accentStyle.Render("Orkestar") + dimStyle.Render("  persistent agent runtime")
	workspacePanel := m.renderWorkspaces()
	terminalPanel := m.renderTerminals()
	taskPanel := m.renderTasks()

	var body string
	if width >= 110 {
		columnWidth := width / 3
		workspacePanel = panelStyle.Width(columnWidth - 4).Render(workspacePanel)
		terminalPanel = panelStyle.Width(columnWidth - 4).Render(terminalPanel)
		taskPanel = panelStyle.Width(width - 2*columnWidth - 5).Render(taskPanel)
		body = lipgloss.JoinHorizontal(lipgloss.Top, workspacePanel, " ", terminalPanel, " ", taskPanel)
	} else {
		workspacePanel = panelStyle.Width(width - 4).Render(workspacePanel)
		terminalPanel = panelStyle.Width(width - 4).Render(terminalPanel)
		taskPanel = panelStyle.Width(width - 4).Render(taskPanel)
		body = workspacePanel + "\n" + terminalPanel + "\n" + taskPanel
	}

	status := ""
	if m.loading {
		status = dimStyle.Render("refreshing...")
	}
	if m.err != nil {
		status = errorStyle.Render(m.err.Error())
	}
	help := dimStyle.Render("n shell  c claude  o opencode  enter attach  tab focus  d diff  r refresh  q detach UI")
	return header + "\n\n" + body + "\n\n" + status + "\n" + help
}

func (m Model) renderTasks() string {
	lines := []string{accentStyle.Render("Tasks")}
	if len(m.snapshot.Tasks) == 0 {
		lines = append(lines, dimStyle.Render("No tasks yet."))
		return strings.Join(lines, "\n")
	}
	for index, task := range m.snapshot.Tasks {
		line := fmt.Sprintf("%-11s  %s", task.Status, task.Title)
		if m.focus == focusTasks && index == m.taskSelected {
			line = selectedStyle.Render(" " + line + " ")
		} else {
			line = "  " + line
		}
		lines = append(lines, line)
		if task.WorktreePath != "" {
			lines = append(lines, dimStyle.Render("    branch "+task.WorktreeBranch))
		}
	}
	return strings.Join(lines, "\n")
}

func (m Model) renderDiff(width int) string {
	header := accentStyle.Render("Diff")
	if m.diffErr != nil {
		return header + "\n\n" + errorStyle.Render(m.diffErr.Error()) + "\n\n" + dimStyle.Render("esc/d/q back")
	}

	lines := []string{accentStyle.Render(fmt.Sprintf("Changed files (%d)", len(m.diff.Files)))}
	for _, file := range m.diff.Files {
		lines = append(lines, fmt.Sprintf("  %s %s", file.Status, file.Path))
	}
	if len(m.diff.Files) == 0 {
		lines = append(lines, dimStyle.Render("  No changes."))
	}
	filesPanel := panelStyle.Width(width - 4).Render(strings.Join(lines, "\n"))

	diffText := m.diff.Diff
	if diffText == "" {
		diffText = dimStyle.Render("No unstaged or staged diff against HEAD.")
	}
	diffPanel := panelStyle.Width(width - 4).Render(diffText)

	return header + "\n\n" + filesPanel + "\n" + diffPanel + "\n\n" + dimStyle.Render("esc/d/q back")
}

func (m Model) renderWorkspaces() string {
	lines := []string{accentStyle.Render("Workspaces")}
	if len(m.snapshot.Workspaces) == 0 {
		lines = append(lines, dimStyle.Render("No workspace yet."), dimStyle.Render("Start an agent to create one."))
		return strings.Join(lines, "\n")
	}
	for _, workspace := range m.snapshot.Workspaces {
		lines = append(lines, fmt.Sprintf("• %s", workspace.Name), dimStyle.Render("  "+workspace.Directory))
	}
	return strings.Join(lines, "\n")
}

func (m Model) renderTerminals() string {
	lines := []string{accentStyle.Render("Sessions")}
	if len(m.snapshot.Terminals) == 0 {
		lines = append(lines, dimStyle.Render("No sessions."), "", "Press c to start Claude Code.")
		return strings.Join(lines, "\n")
	}
	for index, terminal := range m.snapshot.Terminals {
		command := strings.Join(terminal.Command, " ")
		line := fmt.Sprintf("%-9s  %s", terminal.State, command)
		if index == m.selected {
			line = selectedStyle.Render(" " + line + " ")
		} else {
			line = "  " + line
		}
		lines = append(lines, line, dimStyle.Render("    "+terminal.ID))
	}
	return strings.Join(lines, "\n")
}

func (m Model) loadSnapshot() tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		var snapshot daemon.Snapshot
		err := m.client.Call(ctx, "system.snapshot", nil, &snapshot)
		return snapshotMsg{snapshot: snapshot, err: err}
	}
}

func (m Model) startTerminal(command []string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		workspaceID := ""
		for _, workspace := range m.snapshot.Workspaces {
			if sameDirectory(workspace.Directory, m.directory) {
				workspaceID = workspace.ID
				break
			}
		}
		if workspaceID == "" {
			var workspace daemon.Workspace
			if err := m.client.Call(ctx, "workspace.create", map[string]string{
				"directory": m.directory,
			}, &workspace); err != nil {
				return terminalStartedMsg{err: err}
			}
			workspaceID = workspace.ID
		}

		var started daemon.Terminal
		err := m.client.Call(ctx, "terminal.start", map[string]any{
			"workspace_id": workspaceID,
			"command":      command,
			"columns":      max(m.width, 80),
			"rows":         max(m.height, 24),
		}, &started)
		return terminalStartedMsg{terminal: started, err: err}
	}
}

func (m Model) loadDiff() tea.Cmd {
	if m.focus != focusTasks || len(m.snapshot.Tasks) == 0 || m.taskSelected >= len(m.snapshot.Tasks) {
		return nil
	}
	taskID := m.snapshot.Tasks[m.taskSelected].ID
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var diff daemon.TaskDiff
		err := m.client.Call(ctx, "task.diff", map[string]string{"task_id": taskID}, &diff)
		return diffMsg{diff: diff, err: err}
	}
}

func (m Model) attachSelected() tea.Cmd {
	if len(m.snapshot.Terminals) == 0 || m.selected >= len(m.snapshot.Terminals) {
		return nil
	}
	command := exec.Command(m.executable, "terminal", "attach", m.snapshot.Terminals[m.selected].ID)
	return tea.ExecProcess(command, func(err error) tea.Msg {
		return attachFinishedMsg{err: err}
	})
}

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(value time.Time) tea.Msg { return tickMsg(value) })
}

func defaultShell() string {
	if shell := os.Getenv("SHELL"); shell != "" {
		return shell
	}
	return "/bin/sh"
}

func sameDirectory(left, right string) bool {
	leftPath, leftErr := filepath.EvalSymlinks(left)
	rightPath, rightErr := filepath.EvalSymlinks(right)
	if leftErr == nil && rightErr == nil {
		return leftPath == rightPath
	}
	return filepath.Clean(left) == filepath.Clean(right)
}
