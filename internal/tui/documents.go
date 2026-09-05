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
	Editor  string   `json:"editor"`
	Command []string `json:"command,omitempty"`
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
	m.addPane(p)
	return p
}
func (m *Model) openReview() tea.Cmd {
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
	if len(m.visiblePanes()) >= 4 {
		m.notice = "Four panes are open. Close one with Ctrl+b q first."
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
	switch key {
	case "o":
		if len(m.visiblePanes()) < 2 {
			m.notice = "Only one pane. Ctrl+b v/s opens a second pane."
		} else {
			m.nextPane()
			m.notice = ""
		}
		return nil, true
	case "v", "s":
		m.stacked = key == "s"
		if len(m.visiblePanes()) < 2 && !m.opening {
			m.opening = true
			return m.startTerminal([]string{defaultShell()}), true
		}
		m.resizePanes()
		m.notice = ""
		return nil, true
	case "d":
		return m.openReview(), true
	case "e":
		if m.roomForPane() {
			return m.startFilePicker(m.paneRoot()), true
		}
		return nil, true
	case "n":
		if !m.opening && m.roomForPane() {
			m.opening = true
			return m.startTerminal([]string{defaultShell()}), true
		}
		return nil, true
	case "[":
		if m.embedded != nil && m.embedded.stream == nil {
			m.notice = "Use the mouse wheel or Page Up/Down to scroll this pane."
			return nil, true
		}
		return m.loadHistory(), true
	case ",":
		m.settingsOpen = true
		return nil, true
	case "x":
		if m.embedded != nil && m.embedded.editor != nil {
			m.removePane(m.embedded)
			m.notice = "Editor discarded"
		}
		return nil, true
	case "q":
		if m.embedded != nil && m.canClose(m.embedded) {
			m.removePane(m.embedded)
		}
		return nil, true
	case "tab":
		m.sidebarFocused = true
		return nil, true
	}
	return nil, false
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
		return m, p.editor.key(k)
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
	if m.settingsOpen {
		return "Editor settings\n\n1  Standard — mouse, Ctrl+S/Z/Y/A/C/X/V\n2  Vim — native Vim keys and mouse\n3  Nano — native Nano keys and mouse\n\nCurrent: " + m.settings.Editor + "\nSaved to " + settingsPath() + "\nChanges apply to files opened afterwards.\nEsc closes. Custom terminal command: edit tui.json."
	}
	matches := m.matches()
	var lines []string
	for i, path := range matches {
		if i == m.fileAt {
			path = selectedStyle.Render(path)
		}
		lines = append(lines, path)
	}
	if len(lines) == 0 {
		lines = append(lines, "No matches. Enter creates the typed path.")
	}
	return "Open or create a text file\n\nWorkspace: " + m.fileRoot + "\n\nPath: " + m.fileName + "▏\n\n" + strings.Join(lines, "\n") + "\n\n↑/↓ select · Enter opens · Esc cancels"

}
func (m Model) updatePrompt(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if k.String() == "esc" {
		m.filePrompt = false
		m.settingsOpen = false
		return m, nil
	}
	if m.settingsOpen {
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
	case tea.KeyEnter:
		if matches := m.matches(); len(matches) > 0 {
			m.fileName = matches[min(m.fileAt, len(matches)-1)]
		}
		m.filePrompt = false
		if strings.TrimSpace(m.fileName) != "" {
			return m, m.openDocument(m.fileRoot, m.fileName)
		}
	case tea.KeyBackspace:
		m.fileAt = 0
		r := []rune(m.fileName)
		if len(r) > 0 {
			m.fileName = string(r[:len(r)-1])
		}
	default:
		if k.Text != "" && k.Mod&(tea.ModCtrl|tea.ModAlt) == 0 {
			m.fileName += k.Text
		} else if k.Mod == 0 && k.Code >= 32 && k.Code < 127 {
			m.fileName += string(k.Code)
		}
	}
	return m, nil
}
