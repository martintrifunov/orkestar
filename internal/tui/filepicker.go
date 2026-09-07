package tui

import (
	"path"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
)

type fileListMsg struct {
	root  string
	paths []string
}

func (m *Model) startFilePicker(root string) tea.Cmd {
	if m.remoteFilesUnavailable() {
		return nil
	}
	m.filePrompt = true
	m.fileRoot = root
	m.fileName = ""
	m.fileDir = ""
	m.filePaths = nil
	m.fileAt = 0
	root = m.fileRoot
	return func() tea.Msg {
		paths, _ := workspaceFiles(root)
		return fileListMsg{root, paths}
	}
}

// With no query, show immediate children. A query searches below the current
// directory and keeps relative paths usable for opening and creating files.
func (m Model) matches() []string {
	var out []string
	seen := map[string]bool{}
	prefix := m.fileDir
	if prefix != "" {
		prefix += "/"
	}
	for _, p := range m.filePaths {
		p = strings.ReplaceAll(p, "\\", "/")
		if !strings.HasPrefix(p, prefix) {
			continue
		}
		p = strings.TrimPrefix(p, prefix)
		if p == "" {
			continue
		}
		if m.fileName == "" {
			if at := strings.Index(p, "/"); at >= 0 {
				p = p[:at+1]
			}
		}
		if strings.Contains(strings.ToLower(p), strings.ToLower(m.fileName)) && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := strings.HasSuffix(out[i], "/"), strings.HasSuffix(out[j], "/")
		if a != b {
			return a
		}
		return out[i] < out[j]
	})
	return out
}

func (m *Model) pickerParent() {
	child := path.Base(m.fileDir) + "/"
	parent := path.Dir(m.fileDir)
	if parent == "." {
		parent = ""
	}
	m.fileDir, m.fileName, m.fileAt = parent, "", 0
	for i, name := range m.matches() {
		if name == child {
			m.fileAt = i
			break
		}
	}
}

func (m *Model) pickerEnter(name string) {
	next := path.Join(m.fileDir, name)
	if next == ".." || strings.HasPrefix(next, "../") || path.IsAbs(next) {
		return
	}
	m.fileDir = next
	if m.fileDir == "." {
		m.fileDir = ""
	}
	m.fileName, m.fileAt = "", 0
}
