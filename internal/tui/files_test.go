package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func viewerModel(t *testing.T, root string) Model {
	t.Helper()
	m := Model{width: 160, height: 44, directory: root}
	if cmd := m.toggleFiles(); cmd != nil {
		if msg, ok := cmd().(filesLoadedMsg); ok {
			m.applyFiles(msg)
		}
	}
	return m
}

func viewerRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, path := range []string{"main.go", "README.md", "internal/tui/model.go", "internal/tui/files.go", "docs/architecture.md"} {
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("package main\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func rowNames(m Model) []string {
	var names []string
	for _, row := range m.fileRows() {
		names = append(names, strings.Repeat(" ", row.depth)+row.node.name)
	}
	return names
}

func TestFileTreeGroupsDirectoriesFirst(t *testing.T) {
	tree := buildFileTree([]string{"z.txt", "internal/tui/model.go", "a.txt", "internal/files.go", "docs/a.md"})
	var names []string
	for _, child := range tree.children {
		names = append(names, child.name)
	}
	if strings.Join(names, ",") != "docs,internal,a.txt,z.txt" {
		t.Fatalf("directories are not grouped first and sorted: %v", names)
	}
	internal := tree.children[1]
	if !internal.dir || internal.path != "internal" || len(internal.children) != 2 {
		t.Fatalf("nested directory is wrong: %+v", internal)
	}
	if internal.children[0].name != "tui" || !internal.children[0].dir || internal.children[1].name != "files.go" {
		t.Fatalf("nested ordering is wrong: %+v", internal.children)
	}
	if leaf := internal.children[0].children[0]; leaf.path != "internal/tui/model.go" || leaf.dir {
		t.Fatalf("leaf path is wrong: %+v", leaf)
	}
}

func TestViewerIsClosedByDefaultAndCostsNothing(t *testing.T) {
	root := viewerRoot(t)
	m := Model{width: 160, height: 44, directory: root}
	if m.filesOpen || m.filesWidth() != 0 || m.renderFiles() != "" {
		t.Fatal("the viewer is not collapsed by default")
	}
	_, _, closedWidth, _ := m.contentArea()
	if m.filesTree != nil || m.filesLoading {
		t.Fatal("a closed viewer read the workspace")
	}
	// A tick must not start a read while it is closed.
	updated, _ := m.Update(tickMsg{})
	if updated.(Model).filesLoading {
		t.Fatal("a closed viewer refreshed on the tick")
	}

	m = viewerModel(t, root)
	if !m.filesOpen || !m.filesFocused || m.filesTree == nil {
		t.Fatal("toggling did not open, focus and read")
	}
	_, _, openWidth, _ := m.contentArea()
	if openWidth != closedWidth-m.filesWidth() {
		t.Fatalf("panes did not give up the viewer's width: %d vs %d", openWidth, closedWidth)
	}
	if cmd := m.toggleFiles(); cmd != nil || m.filesOpen || m.filesFocused {
		t.Fatal("toggling again did not close the viewer")
	}
	if _, _, width, _ := m.contentArea(); width != closedWidth {
		t.Fatal("closing did not give the width back")
	}
}

func TestViewerMatchesTheSidebarBoxAndFitsTheWindow(t *testing.T) {
	m := viewerModel(t, viewerRoot(t))
	box := m.renderFiles()
	_, _, _, contentHeight := m.contentArea()
	if lipgloss.Width(box) != embeddedSidebarWidth(m.width) {
		t.Fatalf("viewer is %d wide, sidebar is %d", lipgloss.Width(box), embeddedSidebarWidth(m.width))
	}
	if lipgloss.Height(box) != contentHeight {
		t.Fatalf("viewer is %d tall, content area is %d", lipgloss.Height(box), contentHeight)
	}
	frame := m.render()
	if lipgloss.Height(frame) != m.height {
		t.Fatalf("frame is %d rows, window is %d", lipgloss.Height(frame), m.height)
	}
	for _, line := range strings.Split(ansi.Strip(frame), "\n") {
		if ansi.StringWidth(line) > m.width {
			t.Fatalf("row is %d cells wide, window is %d: %q", ansi.StringWidth(line), m.width, line)
		}
	}
	if !strings.Contains(ansi.Strip(frame), "Files") || !strings.Contains(ansi.Strip(frame), "Workspaces") {
		t.Fatal("the frame lost a panel")
	}
}

func TestViewerNavigatesExpandsAndOpensFiles(t *testing.T) {
	root := viewerRoot(t)
	m := viewerModel(t, root)
	if got := rowNames(m); strings.Join(got, ",") != "docs,internal,README.md,main.go" {
		t.Fatalf("collapsed tree is wrong: %v", got)
	}
	key := func(name string) tea.Cmd {
		updated, cmd := m.updateFiles(tea.KeyPressMsg{Code: keyCodeFor(name)})
		m = updated.(Model)
		return cmd
	}
	key("down")
	if m.filesCursor != "internal" {
		t.Fatalf("cursor did not move: %q", m.filesCursor)
	}
	key("right")
	if got := rowNames(m); strings.Join(got, ",") != "docs,internal, tui,README.md,main.go" {
		t.Fatalf("expanding did not reveal children: %v", got)
	}
	key("down")
	key("right")
	if !m.filesExpanded["internal/tui"] {
		t.Fatal("nested directory did not expand")
	}
	// Left collapses, then steps out to the parent.
	key("left")
	if m.filesExpanded["internal/tui"] {
		t.Fatal("left did not collapse")
	}
	key("left")
	if m.filesCursor != "internal" {
		t.Fatalf("left did not step out to the parent: %q", m.filesCursor)
	}
	// Enter on a file opens it in an editor pane.
	m.filesExpanded["internal"] = true
	m.filesExpanded["internal/tui"] = true
	m.filesCursor = "internal/tui/files.go"
	cmd := key("enter")
	if cmd == nil {
		t.Fatal("enter on a file opened nothing")
	}
	msg, ok := cmd().(documentLoaded)
	if !ok || msg.err != nil {
		t.Fatalf("opening failed: %+v", msg)
	}
	updated, _ := m.Update(msg)
	m = updated.(Model)
	if len(m.visiblePanes()) != 1 || m.embedded.editor == nil {
		t.Fatal("no editor pane was opened")
	}
	if !strings.HasSuffix(m.embedded.editor.doc.Path, filepath.Join("internal", "tui", "files.go")) {
		t.Fatalf("opened the wrong file: %s", m.embedded.editor.doc.Path)
	}
}

func TestRefreshKeepsExpansionAndCursorAsFilesChange(t *testing.T) {
	root := viewerRoot(t)
	m := viewerModel(t, root)
	m.filesExpanded["internal"] = true
	m.filesExpanded["internal/tui"] = true
	m.filesCursor = "internal/tui/model.go"
	before := m.fileCursorIndex(m.fileRows())

	// An agent adds a file above the selection and removes another.
	if err := os.WriteFile(filepath.Join(root, "internal", "tui", "aaa.go"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "README.md")); err != nil {
		t.Fatal(err)
	}
	cmd := m.loadFiles()
	if cmd == nil {
		t.Fatal("no refresh was scheduled")
	}
	m.applyFiles(cmd().(filesLoadedMsg))

	if !m.filesExpanded["internal"] || !m.filesExpanded["internal/tui"] {
		t.Fatal("refresh collapsed the tree")
	}
	if m.filesCursor != "internal/tui/model.go" {
		t.Fatalf("refresh moved the cursor: %q", m.filesCursor)
	}
	names := strings.Join(rowNames(m), ",")
	if !strings.Contains(names, "aaa.go") || strings.Contains(names, "README.md") {
		t.Fatalf("refresh did not pick up the change: %v", names)
	}
	if m.fileCursorIndex(m.fileRows()) == before {
		t.Fatal("the fixture did not actually shift the selection's row")
	}
	if m.filesLoading {
		t.Fatal("the refresh never finished")
	}
}

func TestViewerRespondsToTheMouse(t *testing.T) {
	m := viewerModel(t, viewerRoot(t))
	left, top, width, _ := m.filesRect()
	if width == 0 {
		t.Fatal("viewer has no box")
	}
	// The second entry is the internal directory; clicking expands it.
	cmd, inside := m.filesClick(left+2, top+3)
	if !inside || cmd != nil {
		t.Fatal("clicking a directory should expand it in place")
	}
	if !m.filesExpanded["internal"] || m.filesCursor != "internal" {
		t.Fatalf("click did not select and expand: %q", m.filesCursor)
	}
	if _, inside := m.filesClick(left-3, top+3); inside {
		t.Fatal("a click outside the viewer was claimed by it")
	}
	// The wheel scrolls it and stays in range.
	for i := 0; i < 50; i++ {
		if !m.filesScroll(left+2, top+3, 3) {
			t.Fatal("the wheel was not accepted over the viewer")
		}
	}
	if m.filesTop > max(0, len(m.fileRows())-m.filesViewRows()) {
		t.Fatalf("scrolled past the end: %d", m.filesTop)
	}
	for i := 0; i < 50; i++ {
		m.filesScroll(left+2, top+3, -3)
	}
	if m.filesTop != 0 {
		t.Fatalf("scrolled above the start: %d", m.filesTop)
	}
}

func TestViewerRefusesToOpenInANarrowWindow(t *testing.T) {
	m := Model{width: 60, height: 30, directory: t.TempDir()}
	if cmd := m.toggleFiles(); cmd != nil {
		t.Fatal("a narrow window started a read")
	}
	if m.filesOpen || !strings.Contains(m.notice, "too narrow") {
		t.Fatalf("narrow window: open=%v notice=%q", m.filesOpen, m.notice)
	}
	// An open viewer hides rather than squeezing the panes when the window
	// shrinks under it.
	wide := viewerModel(t, viewerRoot(t))
	wide.width = 60
	if wide.filesWidth() != 0 || wide.renderFiles() != "" {
		t.Fatal("the viewer kept its width in a window with no room")
	}
	if _, _, width, _ := wide.contentArea(); width != wide.width-embeddedSidebarWidth(wide.width)-1 {
		t.Fatal("panes did not reclaim the hidden viewer's width")
	}
}

func TestWorkspaceFilesIncludesIgnoredAndEmptyDirectories(t *testing.T) {
	root := viewerRoot(t)
	if out, err := exec.Command("git", "init", root).CombinedOutput(); err != nil {
		t.Fatalf("%s: %v", out, err)
	}
	for _, dir := range []string{"node_modules/package", "empty"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	for name, content := range map[string]string{".gitignore": "ignored.txt\nnode_modules/\n", "ignored.txt": "hidden", "node_modules/package/index.js": "code"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	m := viewerModel(t, root)
	m.filesExpanded["node_modules"] = true
	m.filesExpanded["node_modules/package"] = true
	names := strings.Join(rowNames(m), ",")
	for _, want := range []string{"ignored.txt", "node_modules", "index.js", "empty"} {
		if !strings.Contains(names, want) {
			t.Fatalf("missing %s: %s", want, names)
		}
	}
	for _, row := range m.fileRows() {
		if row.node.name == ".git" {
			t.Fatal("Git database exposed")
		}
		if row.node.name == "empty" && !row.node.dir {
			t.Fatal("empty directory is a file")
		}
	}
}

// keyCodeFor maps the few key names these tests use onto their key codes.
func keyCodeFor(name string) rune {
	switch name {
	case "up":
		return tea.KeyUp
	case "down":
		return tea.KeyDown
	case "left":
		return tea.KeyLeft
	case "right":
		return tea.KeyRight
	case "enter":
		return tea.KeyEnter
	}
	return rune(name[0])
}

// The viewer used to keep focus after opening a file and while a full-screen
// overlay was up, so its keys swallowed Esc and the arrows meant for those.
func TestViewerFocusIsExclusive(t *testing.T) {
	root := viewerRoot(t)
	m := viewerModel(t, root)
	m.client = nil

	// A full-area overlay owns the keyboard even while the viewer has focus.
	m.viewingHistory = true
	m.history = []string{"one", "two", "three"}
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(Model)
	if m.viewingHistory {
		t.Fatal("esc did not close scrollback while the file viewer had focus")
	}
	if !m.filesFocused {
		t.Fatal("closing the overlay should leave the viewer as it was")
	}
	// Now esc returns from the viewer itself.
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = updated.(Model)
	if m.filesFocused {
		t.Fatal("esc did not hand focus back from the viewer")
	}

	// Opening a file moves focus to the new pane, so later keys edit rather
	// than move the tree.
	m.filesFocused = true
	m.filesExpanded["internal"] = true
	m.filesExpanded["internal/tui"] = true
	m.filesCursor = "internal/tui/files.go"
	rows := m.fileRows()
	cmd := m.activateFile(rows[m.fileCursorIndex(rows)].node)
	if cmd == nil {
		t.Fatal("enter opened nothing")
	}
	updated, _ = m.Update(cmd().(documentLoaded))
	m = updated.(Model)
	if m.filesFocused || m.embedded == nil || m.embedded.editor == nil {
		t.Fatalf("focus did not move to the editor: filesFocused=%v", m.filesFocused)
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'x'})
	m = updated.(Model)
	if !strings.Contains(string(m.embedded.editor.text), "x") {
		t.Fatal("typing went to the tree instead of the editor it just opened")
	}
}

// A file read beside a tree wants the full width, so it opens below the
// focused pane rather than beside it.
func TestOpeningAFileFromTheViewerSplitsHorizontally(t *testing.T) {
	root := viewerRoot(t)
	m := viewerModel(t, root)
	m.addPane(fakePane(t, "shell"))
	m.filesFocused = true
	shell := m.embedded

	m.filesCursor = "main.go"
	rows := m.fileRows()
	cmd := m.activateFile(rows[m.fileCursorIndex(rows)].node)
	if cmd == nil {
		t.Fatal("opening the file produced no command")
	}
	if m.documentSplit == nil || !m.documentSplit.stacked || m.documentSplit.target != shell {
		t.Fatalf("the placement was not recorded: %+v", m.documentSplit)
	}
	updated, _ := m.Update(cmd().(documentLoaded))
	m = updated.(Model)
	if m.documentSplit != nil {
		t.Fatal("the placement was not consumed")
	}
	var editor, terminal paneRect
	for _, r := range m.paneRects() {
		if r.terminal.editor != nil {
			editor = r
		} else {
			terminal = r
		}
	}
	if editor.terminal == nil || terminal.terminal == nil {
		t.Fatalf("expected a shell and an editor: %+v", m.paneRects())
	}
	if editor.x != terminal.x || editor.width != terminal.width {
		t.Fatalf("the editor did not take the full width below: editor %+v shell %+v", editor, terminal)
	}
	if editor.y <= terminal.y {
		t.Fatalf("the editor did not open below the focused pane: editor %+v shell %+v", editor, terminal)
	}
}
