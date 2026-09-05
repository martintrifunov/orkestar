package tui

import (
	"context"
	"fmt"
	"os"
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
	clipboardTarget          *textEditor
	filePaths                []string
	fileAt                   int
	prefix                   bool
	notice                   string
	localSequence            int
	settings                 editorSettings
	filePrompt, settingsOpen bool
	fileName, fileRoot       string

	// taskPrompt collects a new task's title. taskBusy blocks a second
	// mutation while one is in flight, because completing a task can run a
	// reviewer agent and take a while.
	taskPrompt, taskReview, taskBusy bool
	taskTitle                        string

	// The file viewer mirrors the sidebar on the right edge. It is closed by
	// default and reads the workspace only while open.
	filesOpen, filesFocused, filesLoading bool
	filesRoot, filesCursor                string
	filesTree                             *fileNode
	filesExpanded                         map[string]bool
	filesTop                              int
	pendingStop                           string
	filesErr                              error
	filesLoadedAt                         time.Time

	viewingHistory bool
	history        []string
	historyOffset  int
	client         *ipc.Client
	directory      string
	snapshot       daemon.Snapshot
	selected       int
	taskSelected   int
	agentSelected  int
	focus          focusPanel
	width          int
	height         int
	loading        bool
	err            error

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

	// The sidebar stays usable while a terminal attachment is visible.
	embedded       *embeddedTerminal
	layout         *splitNode
	pendingSplit   *splitRequest
	sidebarFocused bool
	opening        bool
	ctx            context.Context
}

func New(client *ipc.Client, directory string) Model {
	return Model{
		settings:  readSettings(),
		client:    client,
		directory: directory,
		loading:   true,
	}
}

func Run(client *ipc.Client, directory string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	model := New(client, directory)
	model.ctx = ctx
	program := tea.NewProgram(model)
	_, err := program.Run()
	return err
}

// prompting reports whether a modal prompt owns the keyboard and the content
// area.
func (m Model) prompting() bool { return m.filePrompt || m.settingsOpen || m.taskPrompt }

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.loadSnapshot(), tick())
}

func (m Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch message := message.(type) {
	case tea.WindowSizeMsg:
		m.width = message.Width
		m.height = message.Height
		m.resizePanes()
	case fileListMsg:
		if m.filePrompt && m.fileRoot == message.root {
			m.filePaths = message.paths
		}
	case documentLoaded:
		m.err = message.err
		if message.err == nil {
			for _, p := range m.visiblePanes() {
				if p.editor != nil && p.editor.doc.Path == message.doc.Path {
					m.embedded = p
					m.sidebarFocused = false
					return m, nil
				}
			}
			if !m.roomForPane() {
				return m, nil
			}
			e := newTextEditor(message.doc)
			e.syntax = m.settings.syntaxEnabled()
			p := m.localPane(message.doc.Path, message.root, e)
			p.editor = e
			return m, e.highlight()
		}
	case highlightedMsg:
		return m, message.editor.applyHighlight(message)
	case lifecycleMsg:
		return m, m.applyLifecycle(message)
	case filesLoadedMsg:
		m.applyFiles(message)
	case reviewLoaded:
		if m.findPane(message.pane.terminalID) == message.pane {
			message.pane.review = message.review
			message.pane.emulator = message.review
			m.resizePanes()
		}
	case tea.ClipboardMsg:
		var cmd tea.Cmd
		if m.embedded != nil && m.embedded.editor == m.clipboardTarget && m.clipboardTarget != nil && !m.sidebarFocused && !m.prompting() {
			m.clipboardTarget.Paste(message.Content)
			cmd = m.clipboardTarget.highlight()
		}
		m.clipboardTarget = nil
		return m, cmd
	case tea.MouseMotionMsg:
		if m.embedded != nil && m.embedded.editor != nil && message.Mouse().Button != tea.MouseLeft {
			m.embedded.editor.dragging = false
		}
		if m.forwardMouse("motion", message.Mouse()) {
			return m, nil
		}
		if m.embedded != nil && m.embedded.editor != nil && m.embedded.editor.dragging && message.Mouse().Button == tea.MouseLeft {
			for _, r := range m.paneRects() {
				if r.terminal == m.embedded {
					m.embedded.editor.click(message.Mouse().X-r.x-2, message.Mouse().Y-r.y-1, true)
				}
			}
		}
	case tea.MouseReleaseMsg:
		if m.forwardMouse("release", message.Mouse()) {
			return m, nil
		}
		if m.embedded != nil && m.embedded.editor != nil {
			m.embedded.editor.dragging = false
		}
	case historyMsg:
		m.err = message.err
		if message.err == nil {
			m.viewingHistory = true
			m.history = message.lines
			m.historyOffset = 0
		}
	case tea.MouseWheelMsg:
		// A terminal that negotiated mouse reporting receives the event itself.
		if m.forwardMouse("wheel", message.Mouse()) {
			return m, nil
		}
		mouse := message.Mouse()
		if mouse.Button != tea.MouseWheelUp && mouse.Button != tea.MouseWheelDown {
			return m, nil
		}
		step := 3
		if mouse.Button == tea.MouseWheelUp {
			step = -3
		}
		if m.viewingHistory {
			m.historyOffset = max(0, min(max(0, len(m.history)-1), m.historyOffset-step))
			return m, nil
		}
		if m.prompting() || m.viewingDiff || m.pickingAgent {
			return m, nil
		}
		if m.filesScroll(mouse.X, mouse.Y, step) {
			return m, nil
		}
		// The wheel scrolls a document pane under the pointer and does nothing
		// over a terminal pane. Scrollback replaces the whole view, so it is an
		// explicit Ctrl+b [ action rather than something a stray wheel movement
		// can trigger.
		for _, rect := range m.paneRects() {
			if mouse.X >= rect.x && mouse.X < rect.x+rect.width && mouse.Y >= rect.y && mouse.Y < rect.y+rect.height {
				if e := rect.terminal.editor; e != nil {
					e.scroll(step)
				}
				if r := rect.terminal.review; r != nil {
					r.scroll(step)
				}
				return m, nil
			}
		}
	case tea.MouseClickMsg:
		return m.mouseClick(message)
	case tea.PasteMsg:
		if m.filePrompt {
			m.fileName += strings.ReplaceAll(message.Content, "\n", "")
			return m, nil
		}
		if m.settingsOpen {
			return m, nil
		}
		if m.embedded != nil && !m.sidebarFocused && !m.viewingDiff && !m.pickingAgent && !m.viewingHistory {
			m.embedded.emulator.Paste(message.Content)
			if m.embedded.editor != nil {
				return m, m.embedded.editor.highlight()
			}
		}
	case tea.KeyPressMsg:
		if m.prompting() {
			return m.updatePrompt(message)
		}
		if message.String() == "f6" {
			cmd, _ := m.paneAction("o")
			return m, cmd
		}
		if m.prefix {
			m.prefix = false
			if cmd, handled := m.paneAction(message.String()); handled {
				return m, cmd
			}
			if m.embedded != nil && !m.sidebarFocused {
				m.embedded.detachPending = true
				return m.updateEmbedded(message)
			}
			return m, nil
		}
		if message.String() == "ctrl+b" && !m.viewingHistory && !m.viewingDiff {
			m.prefix = true
			return m, nil
		}
		if m.embedded != nil && !m.sidebarFocused && !m.viewingHistory && !m.viewingDiff && (m.embedded.editor != nil || m.embedded.review != nil) {
			return m.updateDocumentKey(message)
		}
		if m.filesFocused {
			return m.updateFiles(message)
		}
		if m.viewingHistory {
			switch message.String() {
			case "esc", "q":
				m.viewingHistory = false
			case "up", "pgup":
				m.historyOffset = min(max(0, len(m.history)-1), m.historyOffset+max(1, m.height-6))
			case "down", "pgdown":
				m.historyOffset = max(0, m.historyOffset-max(1, m.height-6))
			}
			return m, nil
		}
		if m.embedded != nil && !m.sidebarFocused && !m.viewingDiff && !m.pickingAgent && !m.viewingHistory {
			return m.updateEmbedded(message)
		}
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
				if m.opening || len(m.snapshot.Adapters) == 0 || !m.roomForPane() {
					return m, nil
				}
				m.opening = true
				return m, m.launchPickedAgent()
			}
			return m, nil
		}
		switch message.String() {
		case "q", "ctrl+c":
			for _, p := range m.visiblePanes() {
				if !m.canClose(p) {
					return m, nil
				}
			}
			m.closePanes()
			return m, tea.Quit
		case "esc":
			if m.embedded != nil {
				m.sidebarFocused = false
			}
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
		case "v", "s", "o":
			cmd, _ := m.paneAction(message.String())
			return m, cmd
		case "e":
			cmd, _ := m.paneAction("e")
			return m, cmd
		case "u":
			if !m.roomForPane() {
				return m, nil
			}
			if cmd := m.resumeSelected(); cmd != nil {
				m.opening = true
				return m, cmd
			}
		case "r":
			m.loading = true
			return m, m.loadSnapshot()
		case "n":
			if m.opening || !m.roomForPane() {
				return m, nil
			}
			m.opening = true
			return m, m.startTerminal([]string{defaultShell()})
		case "a":
			m.pickingAgent = true
			if m.agentPickerAt >= len(m.snapshot.Adapters) {
				m.agentPickerAt = 0
			}
			return m, nil
		case "enter":
			if m.opening {
				return m, nil
			}
			id := m.selectedTerminalID()
			if id == "" {
				return m, nil
			}
			if pane := m.findPane(id); pane != nil {
				m.embedded = pane
				m.resizePanes()
				m.sidebarFocused = false
				return m, nil
			}
			if !m.roomForPane() {
				return m, nil
			}
			m.opening = true
			return m, m.openTerminal(id)
		case "X", "shift+x":
			return m, m.stopOrRemoveSelected()
		case "i":
			return m, m.interruptSelected()
		case "f":
			return m, m.toggleFiles()
		case "c":
			if m.focus == focusTasks || len(m.snapshot.Tasks) == 0 {
				m.startTaskPrompt()
			}
			return m, nil
		case "d":
			// On a task this is the task's own diff and reviewer verdict.
			// Elsewhere it is the workspace review pane.
			if m.focus == focusTasks {
				return m, m.loadDiff()
			}
			return m, m.openReview()
		case "m":
			if task, ok := m.selectedTask(); ok && !m.taskBusy {
				m.taskBusy = true
				m.notice = "Marking done…"
				if task.AutoReview {
					m.notice = "Reviewing before done… a reviewer agent is running"
				}
				return m, m.markSelectedTaskDone()
			}
			return m, nil
		case "w":
			if !m.taskBusy {
				if cmd := m.toggleSelectedWorktree(); cmd != nil {
					m.taskBusy = true
					m.notice = "Updating worktree…"
					return m, cmd
				}
			}
			return m, nil
		case "t":
			if !m.taskBusy {
				return m, m.assignSelectedTask()
			}
			return m, nil
		case "y":
			return m, m.resolveSelectedPermission("allow")
		case "x":
			// Deny is for a permission request; with Tasks focused the same
			// key cancels the selected task.
			if m.focus == focusTasks {
				if cmd := m.cancelSelectedTask(); cmd != nil && !m.taskBusy {
					m.taskBusy = true
					return m, cmd
				}
				return m, nil
			}
			return m, m.resolveSelectedPermission("deny")
		}
	case snapshotMsg:
		m.loading = false
		m.err = message.err
		if message.err == nil {
			m.snapshot = message.snapshot
			if m.selected >= len(m.snapshot.Terminals) && m.selected > 0 {
				m.selected = max(0, len(m.snapshot.Terminals)-1)
			}
			if m.taskSelected >= len(m.snapshot.Tasks) && m.taskSelected > 0 {
				m.taskSelected = max(0, len(m.snapshot.Tasks)-1)
			}
			if m.agentSelected >= len(m.snapshot.Agents) && m.agentSelected > 0 {
				m.agentSelected = max(0, len(m.snapshot.Agents)-1)
			}
		}
	case terminalStartedMsg:
		m.opening = false
		m.loading = false
		m.err = message.err
		if message.err != nil {
			m.pendingSplit = nil
		}
		if message.err == nil {
			m.snapshot.Terminals = append(m.snapshot.Terminals, message.terminal)
			m.selected = max(0, len(m.snapshot.Terminals)-1)
			m.opening = true
			return m, m.openTerminal(message.terminal.ID)
		}
	case embeddedReadyMsg:
		m.opening = false
		split := m.pendingSplit
		m.pendingSplit = nil
		debugf("embeddedReadyMsg: err=%v terminal=%v", message.err, message.terminal != nil)
		m.err = message.err
		if message.err != nil {
			return m, nil
		}
		if !m.roomForPane() {
			// The process keeps running in the daemon; only this attachment ends.
			message.terminal.close()
			return m, nil
		}
		if split != nil {
			m.insertPane(message.terminal, split.target, split.stacked)
		} else {
			m.addPane(message.terminal)
		}
		return m, waitEmbeddedEvent(message.terminal)
	case embeddedEventMsg:
		if m.findPane(message.terminalID) != message.terminal {
			debugf("embeddedEventMsg: stale/mismatched, dropping (embedded=%v msgID=%s)", m.embedded != nil, message.terminalID)
			return m, nil
		}
		if message.exited {
			debugf("embeddedEventMsg: exited err=%v", message.err)
			message.terminal.close()
			message.terminal.exited = true
			m.loading = true
			m.err = message.err
			return m, m.loadSnapshot()
		}
		if message.err != nil {
			m.err = message.err
		}
		return m, waitEmbeddedEvent(message.terminal)
	case tickMsg:
		commands := []tea.Cmd{m.loadSnapshot(), tick()}
		if m.filesOpen && !m.filesLoading && time.Since(m.filesLoadedAt) >= filesRefresh {
			commands = append(commands, m.loadFiles())
		}
		return m, tea.Batch(commands...)
	case diffMsg:
		m.err = message.err
		m.diffErr = message.err
		m.diffTaskID = message.taskID
		if message.err == nil {
			m.diff = message.diff
			m.viewingDiff = true
		}
	case taskActionMsg:
		m.taskBusy = false
		m.err = message.err
		m.notice = ""
		if message.err != nil {
			return m, nil
		}
		m.notice = message.notice
		m.focus = focusTasks
		m.loading = true
		return m, m.loadSnapshot()
	case permissionActionMsg:
		m.err = message.err
		if message.err == nil {
			m.loading = true
			return m, m.loadSnapshot()
		}
	case agentLaunchedMsg:
		m.opening = false
		m.err = message.err
		if message.err != nil {
			return m, nil
		}
		m.snapshot.Agents = append(m.snapshot.Agents, message.agent)
		m.agentSelected = len(m.snapshot.Agents) - 1
		if message.agent.TerminalID != "" {
			m.opening = true
			return m, m.openTerminal(message.agent.TerminalID)
		}
		m.loading = true
		return m, m.loadSnapshot()
	default:
		debugf("update: unhandled message type %T embedded=%v", message, m.embedded != nil)
	}
	return m, nil
}

func (m Model) View() tea.View {
	content := m.render()
	view := tea.NewView(content)
	view.AltScreen = true
	view.WindowTitle = "Orkestar"
	view.MouseMode = tea.MouseModeCellMotion
	if m.embedded != nil && !m.sidebarFocused && !m.filesFocused && !m.pickingAgent && !m.viewingDiff && !m.viewingHistory && !m.prompting() && m.width >= 50 && m.height >= 16 {
		x, y, visible := m.embedded.emulator.Cursor()
		if visible {
			for _, r := range m.paneRects() {
				if r.terminal == m.embedded && x >= 0 && y >= 0 && x < r.width-4 && y < r.height-2 {
					view.Cursor = tea.NewCursor(r.x+2+x, r.y+1+y)
				}
			}
		}
	}
	return view
}

func (m Model) render() string {
	return m.renderEmbedded(m.width, m.height)
}

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

func (m Model) renderAgents() string {
	lines := []string{accentStyle.Render("Agents")}
	if len(m.snapshot.Agents) == 0 {
		lines = append(lines, dimStyle.Render("No agent sessions."))
	}
	for index, agent := range m.snapshot.Agents {
		line := fmt.Sprintf("%s  %s", agent.Adapter, agent.State)
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
	for _, task := range m.snapshot.Tasks {
		if task.ID == m.diffTaskID {
			header = accentStyle.Render("Diff · "+task.Title) + dimStyle.Render("  "+string(task.Status))
			if task.AutoReview {
				header += dimStyle.Render(" · review required")
			}
		}
	}
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

func (m Model) selectedTerminalID() string {
	switch m.focus {
	case focusAgents:
		if m.agentSelected >= 0 && m.agentSelected < len(m.snapshot.Agents) {
			return m.snapshot.Agents[m.agentSelected].TerminalID
		}
	case focusSessions:
		if m.selected >= 0 && m.selected < len(m.snapshot.Terminals) {
			return m.snapshot.Terminals[m.selected].ID
		}
	}
	return ""
}

func (m Model) attachSelected() tea.Cmd {
	if id := m.selectedTerminalID(); id != "" {
		return m.openTerminal(id)
	}
	return nil
}

func (m Model) openTerminal(id string) tea.Cmd {
	ctx := m.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	columns, rows := embeddedPaneSize(m.width, m.height)
	return openEmbeddedTerminalContext(ctx, m.client, id, columns, rows)
}

// updateEmbedded handles a key press while an embedded terminal pane is
// open. Global pane actions are handled by Update; remaining prefix commands
// and ordinary keys are routed here for terminal input.
func (m Model) updateEmbedded(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	term := m.embedded

	if term.detachPending {
		term.detachPending = false
		if msg.String() == "q" {
			debugf("updateEmbedded: detaching terminalID=%s", term.terminalID)
			m.removePane(term)
			m.loading = true
			return m, m.loadSnapshot()
		}
		if msg.String() == "[" {
			return m, m.loadHistory()
		}
		if msg.String() == "o" {
			m.nextPane()
			return m, nil
		}
		if msg.String() == "t" {
			m.claimPane()
			return m, nil
		}
		if msg.String() == "a" {
			m.sidebarFocused = true
			m.pickingAgent = true
			return m, nil
		}
		if msg.String() == "tab" {
			m.sidebarFocused = true
			return m, nil
		}
		if msg.Mod&tea.ModCtrl != 0 && msg.Code == 'b' {
			term.emulator.Input([]byte{0x02})
			return m, nil
		}
		// The user meant a literal ctrl+b followed by this key, not a
		// detach: forward both instead of swallowing the ctrl+b.
		term.emulator.Input([]byte{0x02})
		if data := encodeKey(msg); len(data) > 0 {
			term.emulator.Input(data)
		}
		return m, nil
	}

	if msg.Mod&tea.ModCtrl != 0 && msg.Code == 'b' {
		debugf("updateEmbedded: ctrl+b seen, arming detach")
		term.detachPending = true
		return m, nil
	}

	if msg.Code == tea.KeyUp || msg.Code == tea.KeyDown || msg.Code == tea.KeyLeft || msg.Code == tea.KeyRight {
		term.emulator.Navigation(msg.Code, int(msg.Mod))
		return m, nil
	}
	data := encodeKey(msg)

	if len(data) > 0 {
		term.emulator.Input(data)
	}
	return m, nil
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
