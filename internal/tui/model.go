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
	"github.com/martintrifunov/orkestar/internal/workflow"
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
	diff   daemon.TaskDiff
	err    error
	taskID string
}

type taskActionMsg struct {
	task workflow.Task
	err  error
}

type permissionActionMsg struct {
	err error
}

type agentLaunchedMsg struct {
	agent daemon.Agent
	err   error
}

type tickMsg time.Time

// focusPanel is which panel arrow keys move the selection cursor in.
type focusPanel int

const (
	focusSessions focusPanel = iota
	focusTasks
	focusAgents
)

type Model struct {
	client        *ipc.Client
	directory     string
	executable    string
	snapshot      daemon.Snapshot
	selected      int
	taskSelected  int
	agentSelected int
	focus         focusPanel
	width         int
	height        int
	loading       bool
	err           error

	viewingDiff bool
	diffTaskID  string
	diff        daemon.TaskDiff
	diffErr     error

	// pickingAgent shows the "choose an agent to launch" overlay, built
	// dynamically from snapshot.Adapters rather than fixed keybindings, so
	// a newly registered adapter (e.g. a future Codex adapter) appears
	// automatically.
	pickingAgent  bool
	agentPickerAt int
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
		if m.pickingAgent {
			switch message.String() {
			case "esc", "q":
				m.pickingAgent = false
			case "up", "k":
				if m.agentPickerAt > 0 {
					m.agentPickerAt--
				}
			case "down", "j":
				if m.agentPickerAt+1 < len(m.snapshot.Adapters) {
					m.agentPickerAt++
				}
			case "enter":
				m.pickingAgent = false
				return m, m.launchPickedAgent()
			}
			return m, nil
		}
		switch message.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "tab":
			switch m.focus {
			case focusSessions:
				m.focus = focusTasks
			case focusTasks:
				m.focus = focusAgents
			default:
				m.focus = focusSessions
			}
		case "up", "k":
			switch m.focus {
			case focusTasks:
				if m.taskSelected > 0 {
					m.taskSelected--
				}
			case focusAgents:
				if m.agentSelected > 0 {
					m.agentSelected--
				}
			default:
				if m.selected > 0 {
					m.selected--
				}
			}
		case "down", "j":
			switch m.focus {
			case focusTasks:
				if m.taskSelected+1 < len(m.snapshot.Tasks) {
					m.taskSelected++
				}
			case focusAgents:
				if m.agentSelected+1 < len(m.snapshot.Agents) {
					m.agentSelected++
				}
			default:
				if m.selected+1 < len(m.snapshot.Terminals) {
					m.selected++
				}
			}
		case "r":
			m.loading = true
			return m, m.loadSnapshot()
		case "n":
			return m, m.startTerminal([]string{defaultShell()})
		case "a":
			m.pickingAgent = true
			if m.agentPickerAt >= len(m.snapshot.Adapters) {
				m.agentPickerAt = 0
			}
			return m, nil
		case "enter":
			return m, m.attachSelected()
		case "d":
			return m, m.loadDiff()
		case "m":
			return m, m.markSelectedTaskDone()
		case "y":
			return m, m.resolveSelectedPermission("allow")
		case "x":
			return m, m.resolveSelectedPermission("deny")
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
			if m.agentSelected >= len(m.snapshot.Agents) && m.agentSelected > 0 {
				m.agentSelected = len(m.snapshot.Agents) - 1
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
		m.diffTaskID = message.taskID
		if message.err == nil {
			m.diff = message.diff
			m.viewingDiff = true
		}
	case taskActionMsg:
		m.err = message.err
		if message.err == nil {
			m.loading = true
			return m, m.loadSnapshot()
		}
	case permissionActionMsg:
		m.err = message.err
		if message.err == nil {
			m.loading = true
			return m, m.loadSnapshot()
		}
	case agentLaunchedMsg:
		m.err = message.err
		if message.err != nil {
			return m, nil
		}
		if message.agent.TerminalID != "" {
			return m, attachTerminal(m.executable, message.agent.TerminalID)
		}
		m.loading = true
		return m, m.loadSnapshot()
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
	if m.pickingAgent {
		return m.renderAgentPicker(width)
	}

	header := accentStyle.Render("Orkestar") + dimStyle.Render("  persistent agent runtime")
	workspacePanel := m.renderWorkspaces()
	terminalPanel := m.renderTerminals()
	taskPanel := m.renderTasks()
	agentPanel := m.renderAgents()

	var body string
	if width >= 110 {
		columnWidth := (width - 5) / 2
		remainder := width - 2*columnWidth - 5
		workspacePanel = panelStyle.Width(columnWidth).Render(workspacePanel)
		terminalPanel = panelStyle.Width(columnWidth + remainder).Render(terminalPanel)
		taskPanel = panelStyle.Width(columnWidth).Render(taskPanel)
		agentPanel = panelStyle.Width(columnWidth + remainder).Render(agentPanel)
		topRow := lipgloss.JoinHorizontal(lipgloss.Top, workspacePanel, " ", terminalPanel)
		bottomRow := lipgloss.JoinHorizontal(lipgloss.Top, taskPanel, " ", agentPanel)
		body = topRow + "\n" + bottomRow
	} else {
		workspacePanel = panelStyle.Width(width - 4).Render(workspacePanel)
		terminalPanel = panelStyle.Width(width - 4).Render(terminalPanel)
		taskPanel = panelStyle.Width(width - 4).Render(taskPanel)
		agentPanel = panelStyle.Width(width - 4).Render(agentPanel)
		body = workspacePanel + "\n" + terminalPanel + "\n" + taskPanel + "\n" + agentPanel
	}

	status := ""
	if m.loading {
		status = dimStyle.Render("refreshing...")
	}
	if m.err != nil {
		status = errorStyle.Render(m.err.Error())
	}
	help := dimStyle.Render("n shell  a new agent  enter attach  tab focus  d diff  m mark done  y/x allow/deny  r refresh  q detach UI")
	return header + "\n\n" + body + "\n\n" + status + "\n" + help
}

// renderAgentPicker shows the list of registered adapters (from
// snapshot.Adapters) to choose from when launching a new agent, so the set
// of choices always matches what the daemon actually has available rather
// than a fixed set of keybindings.
func (m Model) renderAgentPicker(width int) string {
	header := accentStyle.Render("New agent") + dimStyle.Render("  choose which agent to launch")

	lines := []string{}
	if len(m.snapshot.Adapters) == 0 {
		lines = append(lines, dimStyle.Render("No agent adapters are registered with the daemon."))
	}
	for index, capabilities := range m.snapshot.Adapters {
		mode := "managed only"
		if capabilities.SupportsInteractive {
			mode = "interactive"
		}
		line := fmt.Sprintf("%-16s  %s", capabilities.Name, mode)
		if index == m.agentPickerAt {
			line = selectedStyle.Render(" " + line + " ")
		} else {
			line = "  " + line
		}
		lines = append(lines, line)
	}
	panel := panelStyle.Width(width - 4).Render(strings.Join(lines, "\n"))

	return header + "\n\n" + panel + "\n\n" + dimStyle.Render("up/down select  enter launch  esc cancel")
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

func (m Model) renderAgents() string {
	lines := []string{accentStyle.Render("Agents")}
	if len(m.snapshot.Agents) == 0 {
		lines = append(lines, dimStyle.Render("No agent sessions."))
	}
	for index, agent := range m.snapshot.Agents {
		line := fmt.Sprintf("%-9s  %-9s  %s", agent.Adapter, agent.State, agent.Mode)
		if m.focus == focusAgents && index == m.agentSelected {
			line = selectedStyle.Render(" " + line + " ")
		} else {
			line = "  " + line
		}
		lines = append(lines, line)
		if agent.AttentionReason != "" {
			lines = append(lines, errorStyle.Render("    "+agent.AttentionReason))
		}
	}

	if len(m.snapshot.Permissions) > 0 {
		lines = append(lines, "", accentStyle.Render("Pending permissions"))
		for _, permission := range m.snapshot.Permissions {
			lines = append(lines, fmt.Sprintf("  %s: %s", permission.AgentID, permission.Reason))
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

	sections := header + "\n\n" + filesPanel + "\n" + diffPanel

	if verdict := m.latestReviewVerdict(); verdict != "" {
		reviewPanel := panelStyle.Width(width - 4).Render(accentStyle.Render("Latest reviewer verdict") + "\n\n" + verdict)
		sections += "\n" + reviewPanel
	}

	return sections + "\n\n" + dimStyle.Render("esc/d/q back")
}

// latestReviewVerdict returns the most recent review artifact's content for
// the task whose diff is currently shown, or "" if there is none.
func (m Model) latestReviewVerdict() string {
	var latest string
	for _, artifact := range m.snapshot.Artifacts {
		if artifact.TaskID == m.diffTaskID && artifact.Kind == workflow.ArtifactReview {
			latest = artifact.Content
		}
	}
	return latest
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
		lines = append(lines, dimStyle.Render("No sessions."), "", dimStyle.Render("Press a to launch an agent."))
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

// ensureWorkspace returns the ID of the workspace rooted at m.directory,
// creating it if it doesn't exist yet.
func (m Model) ensureWorkspace(ctx context.Context) (string, error) {
	for _, workspace := range m.snapshot.Workspaces {
		if sameDirectory(workspace.Directory, m.directory) {
			return workspace.ID, nil
		}
	}
	var workspace daemon.Workspace
	if err := m.client.Call(ctx, "workspace.create", map[string]string{
		"directory": m.directory,
	}, &workspace); err != nil {
		return "", err
	}
	return workspace.ID, nil
}

func (m Model) startTerminal(command []string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		workspaceID, err := m.ensureWorkspace(ctx)
		if err != nil {
			return terminalStartedMsg{err: err}
		}

		var started daemon.Terminal
		err = m.client.Call(ctx, "terminal.start", map[string]any{
			"workspace_id": workspaceID,
			"command":      command,
			"columns":      max(m.width, 80),
			"rows":         max(m.height, 24),
		}, &started)
		return terminalStartedMsg{terminal: started, err: err}
	}
}

// launchPickedAgent launches the adapter currently selected in the agent
// picker overlay. Interactive-capable adapters get an interactive session
// (bridged to a terminal the TUI then attaches to); adapters that only
// support managed mode get a managed session, visible in the Agents panel
// but with no terminal to attach to.
func (m Model) launchPickedAgent() tea.Cmd {
	if len(m.snapshot.Adapters) == 0 || m.agentPickerAt >= len(m.snapshot.Adapters) {
		return nil
	}
	capabilities := m.snapshot.Adapters[m.agentPickerAt]
	mode := "managed"
	if capabilities.SupportsInteractive {
		mode = "interactive"
	}

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		workspaceID, err := m.ensureWorkspace(ctx)
		if err != nil {
			return agentLaunchedMsg{err: err}
		}

		var launched daemon.Agent
		err = m.client.Call(ctx, "agent.launch", map[string]any{
			"workspace_id": workspaceID,
			"adapter":      capabilities.Name,
			"mode":         mode,
			"columns":      max(m.width, 80),
			"rows":         max(m.height, 24),
		}, &launched)
		return agentLaunchedMsg{agent: launched, err: err}
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
		return diffMsg{diff: diff, err: err, taskID: taskID}
	}
}

func (m Model) markSelectedTaskDone() tea.Cmd {
	if m.focus != focusTasks || len(m.snapshot.Tasks) == 0 || m.taskSelected >= len(m.snapshot.Tasks) {
		return nil
	}
	taskID := m.snapshot.Tasks[m.taskSelected].ID
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		var task workflow.Task
		err := m.client.Call(ctx, "task.setStatus", map[string]string{
			"task_id": taskID,
			"status":  string(workflow.StatusDone),
		}, &task)
		return taskActionMsg{task: task, err: err}
	}
}

func (m Model) resolveSelectedPermission(decision string) tea.Cmd {
	if m.focus != focusAgents || len(m.snapshot.Agents) == 0 || m.agentSelected >= len(m.snapshot.Agents) {
		return nil
	}
	agentID := m.snapshot.Agents[m.agentSelected].ID
	var permissionID string
	for _, permission := range m.snapshot.Permissions {
		if permission.AgentID == agentID {
			permissionID = permission.ID
			break
		}
	}
	if permissionID == "" {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var result map[string]string
		err := m.client.Call(ctx, "permission.resolve", map[string]string{
			"permission_id": permissionID,
			"decision":      decision,
		}, &result)
		return permissionActionMsg{err: err}
	}
}

func (m Model) attachSelected() tea.Cmd {
	if len(m.snapshot.Terminals) == 0 || m.selected >= len(m.snapshot.Terminals) {
		return nil
	}
	return attachTerminal(m.executable, m.snapshot.Terminals[m.selected].ID)
}

// attachTerminal runs `orkestar terminal attach <id>` as a subprocess,
// taking over the screen until the user detaches (ctrl+b q) or the process
// exits, then returns control to the dashboard.
func attachTerminal(executable, terminalID string) tea.Cmd {
	command := exec.Command(executable, "terminal", "attach", terminalID)
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
