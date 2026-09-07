package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/martintrifunov/orkestar/internal/daemon"
	"github.com/martintrifunov/orkestar/internal/files"
)

type editorSettings struct {
	Editor   string   `json:"editor"`
	Command  []string `json:"command,omitempty"`
	MaxPanes int      `json:"max_panes,omitempty"`
	// Syntax is a pointer so an absent key means on, and "syntax": false in
	// tui.json is distinguishable from the zero value.
	Syntax *bool `json:"syntax,omitempty"`
	// Keys rebinds actions by name. Anything absent keeps its default, so a
	// user changes only what their fingers already expect.
	Keys map[string]string `json:"keys,omitempty"`
	// Bell is the same shape as Syntax: absent means on. It rings the
	// terminal bell when a task finishes or an agent working one stops
	// unexpectedly, which are the two moments worth looking up for.
	Bell *bool `json:"bell,omitempty"`
	// Notifications posts those same two moments to the desktop, and only
	// while the terminal is not focused.
	Notifications *bool `json:"notifications,omitempty"`
}

func (s editorSettings) syntaxEnabled() bool { return s.Syntax == nil || *s.Syntax }
func (s editorSettings) bellEnabled() bool   { return s.Bell == nil || *s.Bell }
func (s editorSettings) notificationsEnabled() bool {
	return s.Notifications == nil || *s.Notifications
}

const (
	defaultMaxPanes = 16
	maxPaneLimit    = 64
)

// paneLimit is the configured open-pane bound. Zero or negative means the
// default; values above the hard ceiling are clamped so a typo cannot ask the
// layout for hundreds of unusable boxes.
func (s editorSettings) paneLimit() int {
	if s.MaxPanes <= 0 {
		return defaultMaxPanes
	}
	return min(s.MaxPanes, maxPaneLimit)
}

func settingsPath() string {
	if p := os.Getenv("ORKESTAR_TUI_CONFIG"); p != "" {
		return p
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "orkestar", "tui.json")
}
func readSettings() editorSettings {
	s := editorSettings{Editor: "standard"}
	b, err := os.ReadFile(settingsPath())
	if err == nil {
		_ = json.Unmarshal(b, &s)
	}
	return s
}
func (s editorSettings) save() error {
	p := settingsPath()
	if p == "" {
		return fmt.Errorf("no config directory")
	}
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, append(b, '\n'), 0600)
}

type documentLoaded struct {
	root string
	doc  *files.Document
	err  error
}

func (m Model) paneRoot() string {
	if m.embedded != nil && !m.sidebarFocused {
		if m.embedded.root != "" {
			return m.embedded.root
		}
		for _, t := range m.snapshot.Terminals {
			if t.ID == m.embedded.terminalID {
				return t.Directory
			}
		}
	}
	if m.focus == focusTasks && m.taskSelected < len(m.snapshot.Tasks) {
		t := m.snapshot.Tasks[m.taskSelected]
		if t.WorktreePath != "" {
			return t.WorktreePath
		}
		for _, w := range m.snapshot.Workspaces {
			if w.ID == t.WorkspaceID {
				return w.Directory
			}
		}
	}
	if m.focus == focusAgents && m.agentSelected < len(m.snapshot.Agents) {
		a := m.snapshot.Agents[m.agentSelected]
		for _, w := range m.snapshot.Workspaces {
			if w.ID == a.WorkspaceID {
				return w.Directory
			}
		}
	}
	return m.directory
}
func (m *Model) localPane(title, root string, screen paneScreen) *embeddedTerminal {
	m.localSequence++
	p := &embeddedTerminal{terminalID: fmt.Sprintf("local-%d", m.localSequence), title: title, root: root, emulator: screen, done: make(chan struct{})}
	if split := m.documentSplit; split != nil {
		m.documentSplit = nil
		m.insertPane(p, split.target, split.stacked)
		return p
	}
	m.addPane(p)
	return p
}
func (m *Model) openReview() tea.Cmd {
	if m.remoteFilesUnavailable() {
		return nil
	}
	root := m.paneRoot()
	for _, p := range m.visiblePanes() {
		if p.review != nil && p.root == root {
			m.embedded = p
			m.sidebarFocused = false
			return m.refreshReview(p, p.review.selected)
		}
	}
	if !m.roomForPane() {
		return nil
	}
	r := &reviewPane{root: root}
	p := m.localPane("Changes", root, r)
	p.review = r
	return m.refreshReview(p, 0)
}
func (m Model) openDocument(root, name string) tea.Cmd {
	if m.client.IsRemote() {
		return func() tea.Msg {
			return documentLoaded{err: fmt.Errorf("file editing is unavailable over SSH; use an editor in a remote shell pane")}
		}
	}
	settings := m.settings
	return func() tea.Msg {
		d, err := files.Open(root, name)
		if err != nil {
			return documentLoaded{err: err}
		}
		if settings.Editor == "standard" || settings.Editor == "" {
			return documentLoaded{root: root, doc: d}
		}
		var command []string
		switch settings.Editor {
		case "vim":
			command = []string{"vim", "-c", "set mouse=a", "--", d.Path}
		case "nano":
			command = []string{"nano", "-m", d.Path}
		case "custom":
			command = append(append([]string(nil), settings.Command...), d.Path)
		default:
			return documentLoaded{err: fmt.Errorf("unknown editor %q", settings.Editor)}
		}
		if len(command) < 2 {
			return documentLoaded{err: fmt.Errorf("configure a terminal editor command")}
		}
		if _, err := exec.LookPath(command[0]); err != nil {
			return documentLoaded{err: err}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var w daemon.Workspace
		err = m.client.Call(ctx, "workspace.create", map[string]string{"directory": root}, &w)
		if err != nil {
			return documentLoaded{err: err}
		}
		var t daemon.Terminal
		err = m.client.Call(ctx, "terminal.start", map[string]any{"workspace_id": w.ID, "command": command, "columns": 80, "rows": 24}, &t)
		return terminalStartedMsg{terminal: t, err: err}
	}
}
func (m *Model) roomForPane() bool {
	limit := m.settings.paneLimit()
	if len(m.visiblePanes()) >= limit {
		m.notice = fmt.Sprintf("%d panes are open (max_panes in tui.json). Close one with Ctrl+b q first.", limit)
		return false
	}
	return true
}
func (m *Model) canClose(p *embeddedTerminal) bool {
	if p != nil && p.editor != nil && p.editor.dirty() {
		m.notice = "Unsaved file: Ctrl+S saves; Ctrl+b x discards this editor."
		return false
	}
	return true
}
func (m *Model) paneAction(key string) (tea.Cmd, bool) {
	// Resizing is the arrows, which are not rebindable: a user who wants a
	// divider moved by other keys is not served by a map.
	switch key {
	case "left", "right", "up", "down":
		m.resizeSplit(key)
		return nil, true
	}
	switch m.keys.prefixAction(key) {
	case ActionNextPane:
		if len(m.visiblePanes()) < 2 {
			m.notice = "Only one pane. Ctrl+b v/s splits it."
		} else {
			m.nextPane()
			m.notice = ""
		}
		return nil, true
	case ActionSplitRight, ActionSplitDown:
		// Every split opens a new daemon-owned shell beside or below
		// the focused pane. The target is captured now so a focus change while
		// the shell starts cannot move the new pane elsewhere.
		if m.opening {
			m.notice = "Still opening the previous pane."
			return nil, true
		}
		if !m.roomForPane() {
			return nil, true
		}
		m.pendingSplit = &splitRequest{target: m.embedded, stacked: m.keys.prefixAction(key) == ActionSplitDown}
		m.opening = true
		m.notice = ""
		return m.startTerminal([]string{defaultShell()}), true
	case ActionZoom:
		if len(m.visiblePanes()) < 2 {
			m.notice = "Only one pane. Ctrl+b v/s splits it."
			return nil, true
		}
		m.zoomed = !m.zoomed
		m.notice = ""
		m.resizePanes()
		return nil, true
	case ActionFiles:
		return m.toggleFiles(), true
	case ActionDiff:
		return m.openReview(), true
	case ActionEditFile:
		if m.roomForPane() {
			return m.startFilePicker(m.paneRoot()), true
		}
		return nil, true
	case ActionNewShell:
		if !m.opening && m.roomForPane() {
			m.opening = true
			return m.startTerminal([]string{defaultShell()}), true
		}
		return nil, true
	case ActionScrollback:
		if m.embedded != nil && m.embedded.stream == nil {
			m.notice = "Use the mouse wheel or Page Up/Down to scroll this pane."
			return nil, true
		}
		return m.loadHistory(), true
	case ActionSettings:
		m.settingsOpen = true
		return nil, true
	case ActionDiscardEdit:
		if m.embedded != nil && m.embedded.editor != nil {
			m.removePane(m.embedded)
			m.notice = "Editor discarded"
		}
		return nil, true
	case ActionClosePane:
		if m.embedded != nil && m.canClose(m.embedded) {
			m.removePane(m.embedded)
		}
		return nil, true
	case ActionRenamePane:
		if m.embedded != nil {
			m.renaming = m.embedded
			m.renameTo = m.embedded.title
		}
		return nil, true
	case ActionClaimPane:
		// Reachable from the sidebar and the right-click menu, not only from a
		// focused pane. A menu entry that silently did nothing would teach the
		// wrong thing about every other entry.
		m.claimPane()
		return nil, true
	case ActionDetachPanel:
		m.sidebarFocused = true
		return nil, true
	}
	if key == "esc" {
		// Ends a repeating resize without the key reaching the terminal.
		return nil, true
	}
	return nil, false
}

// repeatsWithPrefix reports the actions that keep the prefix armed, so they can
// be pressed several times in a row.
func repeatsWithPrefix(key string) bool {
	switch key {
	case "left", "right", "up", "down":
		return true
	}
	return false
}
func (m Model) updateDocumentKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	p := m.embedded
	if p.editor != nil {
		if k.String() == "ctrl+p" {
			cmd := m.startFilePicker(p.root)
			return m, cmd
		}
		if k.String() == "ctrl+s" {
			m.notice = ""
		}
		if k.String() == "ctrl+v" {
			m.clipboardTarget = p.editor
		}
		return m, tea.Batch(p.editor.key(k), p.editor.highlight())
	}
	if p.review != nil {
		r := p.review
		switch k.String() {
		case "r":
			return m, m.refreshReview(p, r.selected)
		case "[":
			return m, m.refreshReview(p, max(0, r.selected-1))
		case "]", "tab":
			return m, m.refreshReview(p, min(max(0, len(r.files)-1), r.selected+1))
		case "e", "enter":
			if len(r.files) > 0 && m.roomForPane() {
				return m, m.openDocument(p.root, r.files[r.selected].path)
			}
		case "up", "k":
			r.scroll(-1)
		case "down", "j":
			r.scroll(1)
		case "pgup":
			r.scroll(-r.rows)
		case "pgdown":
			r.scroll(r.rows)
		}
		return m, nil
	}
	return m, nil
}
func (m Model) promptView() string {
	if m.renaming != nil {
		return "Rename pane\n\nName: " + m.renameTo + "▏" +
			"\n\nEnter renames · an empty name restores the default · Esc cancels"
	}
	if m.taskPrompt {
		return m.taskPromptView()
	}
	if m.settingsOpen {
		syntax := "on"
		if !m.settings.syntaxEnabled() {
			syntax = "off"
		}
		bell := "on"
		if !m.settings.bellEnabled() {
			bell = "off"
		}
		notifications := "on"
		if !m.settings.notificationsEnabled() {
			notifications = "off"
		}
		if !notificationsSupported() {
			notifications = "unavailable on this system"
		}
		return "Settings\n\n1  Standard — mouse, Ctrl+S/Z/Y/A/C/X/V\n2  Vim — native Vim keys and mouse\n3  Nano — native Nano keys and mouse\n\nh  Syntax highlighting: " + syntax + " — applies to open files too\nb  Bell: " + bell + " — rings when a task finishes or its agent stops\nn  Notifications: " + notifications + " — the same two moments, when the terminal is not focused\n\nCurrent editor: " + m.settings.Editor + "\nSaved to " + settingsPath() + "\nEditor changes apply to files opened afterwards.\nEsc closes. Key bindings, custom terminal command and max_panes: edit tui.json.\nBindings are \"keys\": {\"prefix\": \"ctrl+a\", \"new-task\": \"N\"} and so on."
	}
	matches := m.matches()
	var lines []string
	visible := max(1, m.height-14)
	top := max(0, m.fileAt-visible+1)
	for i := top; i < min(len(matches), top+visible); i++ {
		path := matches[i]
		if i == m.fileAt {
			path = selectedStyle.Render(path)
		}
		lines = append(lines, path)
	}
	if len(lines) == 0 {
		lines = append(lines, "No matches. Enter creates the typed path.")
	}
	return "Open or create a text file\n\nWorkspace: " + m.fileRoot + "\nDirectory: /" + m.fileDir + "\n\nPath: " + m.fileName + "▏\n\n" + strings.Join(lines, "\n") + "\n\n↑/↓ select · →/Enter opens · ← goes up · Esc cancels"

}
func (m Model) updatePrompt(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.renaming != nil {
		return m.updateRenamePrompt(k)
	}
	if m.taskPrompt {
		return m.updateTaskPrompt(k)
	}
	if k.String() == "esc" {
		m.filePrompt = false
		m.settingsOpen = false
		return m, nil
	}
	if m.settingsOpen {
		if k.String() == "h" {
			on := !m.settings.syntaxEnabled()
			m.settings.Syntax = &on
			if m.err = m.settings.save(); m.err != nil {
				return m, nil
			}
			m.settingsOpen = false
			m.notice = "Syntax highlighting: off"
			if on {
				m.notice = "Syntax highlighting: on"
			}
			var cmds []tea.Cmd
			for _, p := range m.visiblePanes() {
				if p.editor != nil {
					cmds = append(cmds, p.editor.setSyntax(on))
				}
			}
			return m, tea.Batch(cmds...)
		}
		if k.String() == "n" {
			on := !m.settings.notificationsEnabled()
			m.settings.Notifications = &on
			if m.err = m.settings.save(); m.err != nil {
				return m, nil
			}
			m.settingsOpen = false
			m.notice = "Notifications: off"
			if on {
				m.notice = "Notifications: on"
			}
			return m, nil
		}
		if k.String() == "b" {
			on := !m.settings.bellEnabled()
			m.settings.Bell = &on
			if m.err = m.settings.save(); m.err != nil {
				return m, nil
			}
			m.settingsOpen = false
			m.notice = "Bell: off"
			if on {
				m.notice = "Bell: on"
			}
			return m, nil
		}
		modes := map[string]string{"1": "standard", "2": "vim", "3": "nano"}
		if mode, ok := modes[k.String()]; ok {
			m.settings.Editor = mode
			m.err = m.settings.save()
			if m.err == nil {
				m.settingsOpen = false
				m.notice = "Editor: " + mode
			}
		}
		return m, nil
	}
	switch k.Code {
	case tea.KeyUp:
		m.fileAt = max(0, m.fileAt-1)
		return m, nil
	case tea.KeyDown:
		m.fileAt = min(max(0, len(m.matches())-1), m.fileAt+1)
		return m, nil
	case tea.KeyLeft:
		m.pickerParent()
		return m, nil
	case tea.KeyRight:
		if matches := m.matches(); len(matches) > 0 {
			name := matches[min(m.fileAt, len(matches)-1)]
			if strings.HasSuffix(name, "/") {
				m.pickerEnter(name)
			}
		}
		return m, nil
	case tea.KeyEnter:
		if matches := m.matches(); len(matches) > 0 {
			m.fileName = matches[min(m.fileAt, len(matches)-1)]
		}
		if strings.HasSuffix(m.fileName, "/") {
			m.pickerEnter(m.fileName)
			return m, nil
		}
		m.filePrompt = false
		if strings.TrimSpace(m.fileName) != "" {
			return m, m.openDocument(m.fileRoot, filepath.Join(m.fileDir, m.fileName))
		}
	case tea.KeyBackspace:
		m.fileAt = 0
		r := []rune(m.fileName)
		if len(r) > 0 {
			m.fileName = string(r[:len(r)-1])
		} else {
			m.pickerParent()
		}
	default:
		m.fileAt = 0
		if k.Text != "" && k.Mod&(tea.ModCtrl|tea.ModAlt) == 0 {
			m.fileName += k.Text
		} else if k.Mod == 0 && k.Code >= 32 && k.Code < 127 {
			m.fileName += string(k.Code)
		}
	}
	return m, nil
}

// updateRenamePrompt collects a pane's new name. The name is the client's own:
// the daemon owns the process and has no opinion about what a pane is called.
func (m Model) updateRenamePrompt(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch k.Code {
	case tea.KeyEscape:
		m.renaming, m.renameTo = nil, ""
		return m, nil
	case tea.KeyEnter:
		// An empty name restores the default rather than leaving a blank
		// border, since a pane with no name at all cannot be told apart.
		m.renaming.title = strings.TrimSpace(m.renameTo)
		m.renaming, m.renameTo = nil, ""
		return m, nil
	case tea.KeyBackspace:
		if r := []rune(m.renameTo); len(r) > 0 {
			m.renameTo = string(r[:len(r)-1])
		}
		return m, nil
	}
	if k.Text != "" && k.Mod&(tea.ModCtrl|tea.ModAlt) == 0 {
		m.renameTo += k.Text
	} else if k.Mod == 0 && k.Code >= 32 && k.Code < 127 {
		m.renameTo += string(k.Code)
	}
	return m, nil
}

func (m *Model) remoteFilesUnavailable() bool {
	if !m.client.IsRemote() {
		return false
	}
	m.notice = "File browsing, editing and Git review are local-only. Use a remote shell pane."
	return true
}
