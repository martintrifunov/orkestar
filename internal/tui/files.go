package tui

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// filesRefresh is how often the open viewer re-reads the workspace. It only
// runs while the viewer is open, so a collapsed viewer costs nothing.
const filesRefresh = 2 * time.Second

var directoryStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#78A9E8"))

// fileNode is one entry of the workspace tree. Paths are relative to the
// viewer's root and always use forward slashes, so they are stable map keys
// for the expansion set and the cursor.
type fileNode struct {
	name, path string
	dir        bool
	children   []*fileNode
}

type fileRow struct {
	node  *fileNode
	depth int
}

type filesLoadedMsg struct {
	root  string
	paths []string
	err   error
}

// workspaceFiles lists the files of a workspace, preferring Git so ignored
// paths stay out, and falling back to a bounded walk outside a repository.
func workspaceFiles(root string) ([]string, error) {
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("cannot read %s", root)
	}
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
		return paths, nil
	}
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
		if rel, e := filepath.Rel(root, path); e == nil {
			paths = append(paths, rel)
		}
		return nil
	})
	return paths, nil
}

// buildFileTree turns a flat path list into a tree. Directories are inferred
// from the paths themselves, so an empty directory simply does not appear.
func buildFileTree(paths []string) *fileNode {
	root := &fileNode{dir: true}
	nodes := map[string]*fileNode{"": root}
	for _, path := range paths {
		parent, prefix := root, ""
		parts := strings.Split(filepath.ToSlash(path), "/")
		for i, part := range parts {
			if part == "" || part == "." {
				continue
			}
			if prefix != "" {
				prefix += "/"
			}
			prefix += part
			node, ok := nodes[prefix]
			if !ok {
				node = &fileNode{name: part, path: prefix, dir: i < len(parts)-1}
				nodes[prefix] = node
				parent.children = append(parent.children, node)
			}
			parent = node
		}
	}
	sortTree(root)
	return root
}

// sortTree orders directories before files, then by name, the way a file
// explorer is expected to read.
func sortTree(n *fileNode) {
	sort.Slice(n.children, func(i, j int) bool {
		a, b := n.children[i], n.children[j]
		if a.dir != b.dir {
			return a.dir
		}
		return a.name < b.name
	})
	for _, child := range n.children {
		sortTree(child)
	}
}

// filesPanelWidth is the viewer's own width, matching the sidebar so the two
// edges of the window balance.
func (m Model) filesPanelWidth() int { return embeddedSidebarWidth(m.width) }

// filesWidth is the horizontal space the viewer takes from the pane area,
// including the gap. It is zero when the viewer is closed, and also when the
// window is too narrow to keep a usable pane beside it.
func (m Model) filesWidth() int {
	if !m.filesOpen {
		return 0
	}
	width := m.filesPanelWidth() + 1
	if m.width-embeddedSidebarWidth(m.width)-1-width < minPaneWidth {
		return 0
	}
	return width
}

// filesRect is the viewer's box in screen coordinates.
func (m Model) filesRect() (x, y, width, height int) {
	if m.filesWidth() == 0 {
		return 0, 0, 0, 0
	}
	_, top, _, contentHeight := m.contentArea()
	return m.width - m.filesPanelWidth(), top, m.filesPanelWidth(), contentHeight
}

// filesViewRows is how many entries fit, after the border and the heading.
func (m Model) filesViewRows() int {
	_, _, _, height := m.contentArea()
	return max(1, height-3)
}

// fileRows flattens the tree into the visible entries, honouring which
// directories are expanded.
func (m Model) fileRows() []fileRow {
	var rows []fileRow
	var walk func(*fileNode, int)
	walk = func(n *fileNode, depth int) {
		for _, child := range n.children {
			rows = append(rows, fileRow{child, depth})
			if child.dir && m.filesExpanded[child.path] {
				walk(child, depth+1)
			}
		}
	}
	if m.filesTree != nil {
		walk(m.filesTree, 0)
	}
	return rows
}

func (m Model) fileCursorIndex(rows []fileRow) int {
	for i, row := range rows {
		if row.node.path == m.filesCursor {
			return i
		}
	}
	return 0
}

// toggleFiles opens or closes the viewer. Opening focuses it and starts the
// first read; both directions resize the panes around it.
func (m *Model) toggleFiles() tea.Cmd {
	if m.filesOpen {
		m.filesOpen, m.filesFocused = false, false
		m.resizePanes()
		return nil
	}
	m.filesOpen = true
	if m.filesWidth() == 0 {
		m.filesOpen = false
		m.notice = "Window too narrow for the file viewer."
		return nil
	}
	m.filesFocused = true
	m.sidebarFocused = false
	m.notice = ""
	if m.filesExpanded == nil {
		m.filesExpanded = map[string]bool{}
	}
	m.filesRoot = m.directory
	m.resizePanes()
	return m.loadFiles()
}

func (m *Model) loadFiles() tea.Cmd {
	if m.filesRoot == "" {
		return nil
	}
	m.filesLoading = true
	root := m.filesRoot
	return func() tea.Msg {
		paths, err := workspaceFiles(root)
		return filesLoadedMsg{root: root, paths: paths, err: err}
	}
}

// applyFiles rebuilds the tree from a completed read. Expansion and the cursor
// are keyed by path, so a refresh that adds or removes files does not move the
// selection or collapse anything.
func (m *Model) applyFiles(msg filesLoadedMsg) {
	m.filesLoading = false
	m.filesLoadedAt = time.Now()
	if msg.root != m.filesRoot {
		return
	}
	m.filesErr = msg.err
	if msg.err != nil {
		return
	}
	m.filesTree = buildFileTree(msg.paths)
}

// revealFile keeps the cursor on screen after it moves.
func (m *Model) revealFile(index int) {
	visible := m.filesViewRows()
	if index < m.filesTop {
		m.filesTop = index
	}
	if index >= m.filesTop+visible {
		m.filesTop = index - visible + 1
	}
	m.filesTop = max(0, m.filesTop)
}

// selectFileRow moves the cursor to a row and scrolls it into view.
func (m *Model) selectFileRow(rows []fileRow, index int) {
	if len(rows) == 0 {
		return
	}
	index = max(0, min(len(rows)-1, index))
	m.filesCursor = rows[index].node.path
	m.revealFile(index)
}

// activateFile expands or collapses a directory, or opens a file in an editor
// pane.
func (m *Model) activateFile(node *fileNode) tea.Cmd {
	if node == nil {
		return nil
	}
	if node.dir {
		m.filesExpanded[node.path] = !m.filesExpanded[node.path]
		return nil
	}
	if !m.roomForPane() {
		return nil
	}
	return m.openDocument(m.filesRoot, node.path)
}

func (m Model) updateFiles(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	rows := m.fileRows()
	index := m.fileCursorIndex(rows)
	switch k.String() {
	case "esc":
		m.filesFocused = false
	case "tab":
		m.filesFocused = false
		m.sidebarFocused = true
	case "up", "k":
		m.selectFileRow(rows, index-1)
	case "down", "j":
		m.selectFileRow(rows, index+1)
	case "pgup":
		m.selectFileRow(rows, index-m.filesViewRows())
	case "pgdown":
		m.selectFileRow(rows, index+m.filesViewRows())
	case "home":
		m.selectFileRow(rows, 0)
	case "end":
		m.selectFileRow(rows, len(rows)-1)
	case "right", "l":
		if index < len(rows) && rows[index].node.dir && !m.filesExpanded[rows[index].node.path] {
			m.filesExpanded[rows[index].node.path] = true
		}
	case "left", "h":
		// Collapse an open directory, otherwise step out to the parent.
		if index < len(rows) {
			node := rows[index].node
			if node.dir && m.filesExpanded[node.path] {
				m.filesExpanded[node.path] = false
				break
			}
			if at := strings.LastIndex(node.path, "/"); at > 0 {
				m.filesCursor = node.path[:at]
				m.revealFile(m.fileCursorIndex(m.fileRows()))
			}
		}
	case "enter":
		if index < len(rows) {
			return m, m.activateFile(rows[index].node)
		}
	case "r":
		return m, m.loadFiles()
	}
	return m, nil
}

func (m Model) renderFiles() string {
	_, _, width, height := m.filesRect()
	if width == 0 {
		return ""
	}
	heading := accentStyle.Render("Files")
	if m.filesLoading && m.filesTree == nil {
		heading += dimStyle.Render(" reading…")
	}
	out := []string{heading}
	switch {
	case m.filesErr != nil:
		out = append(out, errorStyle.Render(m.filesErr.Error()))
	case m.filesTree == nil:
		out = append(out, dimStyle.Render("Reading the workspace…"))
	default:
		rows := m.fileRows()
		if len(rows) == 0 {
			out = append(out, dimStyle.Render("No files."))
		}
		cursor := m.fileCursorIndex(rows)
		visible := m.filesViewRows()
		top := max(0, min(m.filesTop, max(0, len(rows)-visible)))
		for i := top; i < min(len(rows), top+visible); i++ {
			row := rows[i]
			marker := "  "
			if row.node.dir {
				marker = "▸ "
				if m.filesExpanded[row.node.path] {
					marker = "▾ "
				}
			}
			line := strings.Repeat("  ", row.depth) + marker + row.node.name
			switch {
			case i == cursor && m.filesFocused:
				line = selectedStyle.Render(line)
			case row.node.dir:
				line = directoryStyle.Render(line)
			}
			out = append(out, line)
		}
	}
	style := panelStyle
	if m.filesFocused {
		style = style.BorderForeground(lipgloss.Color("#D7A84B"))
	}
	return style.Width(width).Height(height).Render(fitPane(strings.Join(out, "\n"), width-4, height-2))
}

// filesClick focuses the viewer and acts on the entry under the pointer. The
// bool reports whether the click landed in the viewer at all.
func (m *Model) filesClick(x, y int) (tea.Cmd, bool) {
	left, top, width, height := m.filesRect()
	if width == 0 || x < left || x >= left+width || y < top || y >= top+height {
		return nil, false
	}
	m.filesFocused = true
	m.sidebarFocused = false
	rows := m.fileRows()
	// The first interior line is the heading, so entries start one below it.
	at := m.filesTop + (y - top - 2)
	if at < 0 || at >= len(rows) {
		return nil, true
	}
	m.selectFileRow(rows, at)
	return m.activateFile(rows[at].node), true
}

// filesScroll moves the viewer when the wheel is over it.
func (m *Model) filesScroll(x, y, step int) bool {
	left, top, width, height := m.filesRect()
	if width == 0 || x < left || x >= left+width || y < top || y >= top+height {
		return false
	}
	rows := len(m.fileRows())
	m.filesTop = max(0, min(max(0, rows-m.filesViewRows()), m.filesTop+step))
	return true
}
