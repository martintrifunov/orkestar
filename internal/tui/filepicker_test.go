package tui

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestPickerBrowsesDirectoriesAndOpensNestedFile(t *testing.T) {
	root := viewerRoot(t)
	m := Model{width: 120, height: 24}
	cmd := m.startFilePicker(root)
	updated, _ := m.Update(cmd())
	m = updated.(Model)
	key := func(code rune) tea.Cmd {
		updated, cmd := m.updatePrompt(tea.KeyPressMsg{Code: code})
		m = updated.(Model)
		return cmd
	}
	if got := strings.Join(m.matches(), ","); got != "docs/,internal/,README.md,main.go" {
		t.Fatal(got)
	}
	key(tea.KeyDown)
	key(tea.KeyRight)
	if m.fileDir != "internal" {
		t.Fatal(m.fileDir)
	}
	key(tea.KeyEnter)
	if m.fileDir != "internal/tui" {
		t.Fatal(m.fileDir)
	}
	key(tea.KeyLeft)
	if m.fileDir != "internal" {
		t.Fatal(m.fileDir)
	}
	key(tea.KeyBackspace)
	if m.fileDir != "" || m.matches()[m.fileAt] != "internal/" {
		t.Fatal("parent lost selection")
	}
	key(tea.KeyRight)
	key(tea.KeyRight)
	cmd = key(tea.KeyEnter)
	if cmd == nil {
		t.Fatal("file did not open")
	}
	msg := cmd().(documentLoaded)
	expected, err := filepath.EvalSymlinks(filepath.Join(root, "internal/tui/files.go"))
	if err != nil {
		t.Fatal(err)
	}
	if msg.err != nil || msg.doc.Path != expected {
		t.Fatalf("%+v", msg)
	}
}

func TestPickerCanReachBeyondEightResultsAndSearch(t *testing.T) {
	m := Model{height: 20, filePrompt: true}
	for i := 0; i < 30; i++ {
		m.filePaths = append(m.filePaths, fmt.Sprintf("file%02d.txt", i))
	}
	for i := 0; i < 29; i++ {
		updated, _ := m.updatePrompt(tea.KeyPressMsg{Code: tea.KeyDown})
		m = updated.(Model)
	}
	if m.fileAt != 29 || !strings.Contains(m.promptView(), "file29.txt") {
		t.Fatal("last file is unreachable")
	}
	m.filePaths = append(m.filePaths, "nested/target.go")
	m.fileName = "target"
	if got := strings.Join(m.matches(), ","); got != "nested/target.go" {
		t.Fatal(got)
	}
}
