package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
)

type fileListMsg struct {
	root  string
	paths []string
}

func (m *Model) startFilePicker(root string) tea.Cmd {
	m.filePrompt = true
	m.fileRoot = root
	m.fileName = ""
	m.filePaths = nil
	m.fileAt = 0
	root = m.fileRoot
	return func() tea.Msg {
		paths, _ := workspaceFiles(root)
		return fileListMsg{root, paths}
	}
}

func (m Model) matches() []string {
	var out []string
	for _, p := range m.filePaths {
		if strings.Contains(strings.ToLower(p), strings.ToLower(m.fileName)) {
			out = append(out, p)
			if len(out) >= 8 {
				break
			}
		}
	}
	return out
}
