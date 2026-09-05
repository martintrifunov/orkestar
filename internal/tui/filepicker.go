package tui

import (
	"io/fs"
	"path/filepath"
	"strings"
	"time"

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
	return func() tea.Msg {
		var paths []string
		if raw, err := gitOutput(root, "ls-files", "--cached", "--others", "--exclude-standard", "-z"); err == nil {
			seen := map[string]bool{}
			for _, path := range strings.Split(raw, "\x00") {
				if path != "" && !seen[path] {
					seen[path] = true
					paths = append(paths, path)
				}
				if len(paths) >= 10000 {
					break
				}
			}
		} else {
			deadline := time.Now().Add(3 * time.Second)
			_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return nil
				}
				if len(paths) >= 10000 || time.Now().After(deadline) {
					return fs.SkipAll
				}
				if d.IsDir() {
					if d.Name() == ".git" || d.Name() == "node_modules" {
						return fs.SkipDir
					}
					return nil
				}
				rel, e := filepath.Rel(root, path)
				if e == nil {
					paths = append(paths, rel)
				}
				return nil
			})
		}
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
