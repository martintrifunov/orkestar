package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/martintrifunov/orkestar/internal/daemon"
	"github.com/martintrifunov/orkestar/internal/ipc"
)

// Portable layouts are the arrangement without the machine: the split
// structure, and per leaf what the pane was showing (its last terminal, label,
// command and directory) rather than only a terminal ID that means nothing on
// another daemon. Applying one reuses a running terminal when it can, and
// starts the saved command when it cannot, so a layout survives a restart or
// another checkout.

type portableLayout struct {
	Version int           `json:"version"`
	Focus   int           `json:"focus"`
	Root    *portableNode `json:"root"`
}

type portableNode struct {
	Stacked bool          `json:"stacked,omitempty"`
	Ratio   float64       `json:"ratio,omitempty"`
	Pane    *portablePane `json:"pane,omitempty"`
	First   *portableNode `json:"first,omitempty"`
	Second  *portableNode `json:"second,omitempty"`
}

type portablePane struct {
	Terminal  string   `json:"terminal,omitempty"`
	Label     string   `json:"label,omitempty"`
	Command   []string `json:"command,omitempty"`
	Directory string   `json:"directory,omitempty"`
}

// layoutSummary is one file in the layouts directory, ready for the overlay.
type layoutSummary struct {
	Name  string
	Panes int
	Err   string
}

// layoutsListedMsg carries the layouts directory back to the overlay.
type layoutsListedMsg struct {
	layouts []layoutSummary
	err     error
}

// layoutActionMsg reports a save, delete or apply that could not install a
// tree of its own.
type layoutActionMsg struct {
	notice    string
	err       error
	machineID string
}

func terminalsByID(terminals []daemon.Terminal) map[string]daemon.Terminal {
	byID := make(map[string]daemon.Terminal, len(terminals))
	for _, terminal := range terminals {
		byID[terminal.ID] = terminal
	}
	return byID
}

// openLayouts shows the portable-layout overlay and reads its directory. A
// session without a client-side runtime directory (none today) has nowhere
// portable to read from.
func (m *Model) openLayouts() tea.Cmd {
	if m.layoutsDir == "" {
		m.notice = "Portable layouts are unavailable in this session."
		return nil
	}
	m.layoutsOpen = true
	m.layoutAt = 0
	m.layouts = nil
	m.layoutsErr = nil
	m.namingLayout = false
	m.layoutName = ""
	return m.loadLayouts()
}

func (m Model) loadLayouts() tea.Cmd {
	directory := m.layoutsDir
	return func() tea.Msg {
		layouts, err := listLayoutFiles(directory)
		return layoutsListedMsg{layouts: layouts, err: err}
	}
}

func (m Model) selectedLayout() (layoutSummary, bool) {
	if m.layoutAt < 0 || m.layoutAt >= len(m.layouts) {
		return layoutSummary{}, false
	}
	return m.layouts[m.layoutAt], true
}

// saveCurrentLayout writes the arrangement under a name. A name already in
// use is replaced rather than refused: saving again under the same name is how
// someone updates their own layout.
func (m Model) saveCurrentLayout(name string) tea.Cmd {
	directory := m.layoutsDir
	layout := portableFromTree(m.tree(), terminalsByID(m.snapshot.Terminals), m.embedded)
	machineID := m.currentMachine().ID
	return func() tea.Msg {
		if layout.Root == nil {
			return layoutActionMsg{err: fmt.Errorf("there is no pane arrangement to save"), machineID: machineID}
		}
		clean, err := layoutName(name)
		if err != nil {
			return layoutActionMsg{err: err, machineID: machineID}
		}
		if err := writePortableLayout(filepath.Join(directory, clean+".json"), layout); err != nil {
			return layoutActionMsg{err: err, machineID: machineID}
		}
		return layoutActionMsg{notice: "Layout " + clean + " saved", machineID: machineID}
	}
}

func (m Model) deleteLayoutFile(name string) tea.Cmd {
	directory := m.layoutsDir
	machineID := m.currentMachine().ID
	return func() tea.Msg {
		path, err := layoutName(name)
		if err != nil {
			return layoutActionMsg{err: err, machineID: machineID}
		}
		if err := os.Remove(filepath.Join(directory, path+".json")); err != nil {
			return layoutActionMsg{err: err, machineID: machineID}
		}
		return layoutActionMsg{notice: "Layout " + path + " removed", machineID: machineID}
	}
}

// applyLayoutFile reads a saved layout and rebuilds it on this machine. The
// caller has already closed the current panes, so the restore is the only
// thing the layout message can install.
func (m Model) applyLayoutFile(name string) tea.Cmd {
	client := m.client
	machineID := m.currentMachine().ID
	directory := m.layoutsDir
	running := append([]daemon.Terminal(nil), m.snapshot.Terminals...)
	columns, rows := embeddedPaneSize(m.width, m.height)
	return func() tea.Msg {
		saved, err := readPortableLayout(filepath.Join(directory, name+".json"))
		if err != nil {
			return layoutActionMsg{err: err, machineID: machineID}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		workspaceID, err := m.ensureWorkspace(ctx)
		if err != nil {
			return layoutActionMsg{err: err, machineID: machineID}
		}
		return applyPortableLayout(ctx, client, machineID, workspaceID, saved, running, columns, rows)
	}
}

func (m Model) layoutsView() string {
	if m.namingLayout {
		lines := []string{"Save layout", "", "Name: " + m.layoutName + "▏"}
		if m.layoutsErr != nil {
			lines = append(lines, "", m.theme.error.Render(m.layoutsErr.Error()))
		}
		return strings.Join(lines, "\n") +
			"\n\nEnter saves the current pane arrangement · Esc cancels"
	}
	lines := []string{"Layouts", ""}
	if len(m.layouts) == 0 {
		lines = append(lines, m.theme.dim.Render("No saved layouts. Press s to save this arrangement."))
	}
	for index, layout := range m.layouts {
		detail := fmt.Sprintf("%d panes", layout.Panes)
		if layout.Err != "" {
			detail = layout.Err
		}
		line := fmt.Sprintf("%-16s %s", layout.Name, detail)
		if index == m.layoutAt {
			line = m.theme.selected.Render(" " + line + " ")
		} else {
			line = "  " + line
		}
		lines = append(lines, line)
	}
	if m.layoutsErr != nil {
		lines = append(lines, "", m.theme.error.Render(m.layoutsErr.Error()))
	}
	return strings.Join(lines, "\n") +
		"\n\nenter apply · s save current · x delete · esc close"
}

func (m Model) updateLayouts(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.namingLayout {
		switch k.Code {
		case tea.KeyEscape:
			m.namingLayout = false
			m.layoutName = ""
			return m, nil
		case tea.KeyEnter:
			name := strings.TrimSpace(m.layoutName)
			if name == "" {
				m.layoutsErr = fmt.Errorf("a layout needs a name")
				return m, nil
			}
			m.namingLayout = false
			m.layoutName = ""
			m.layoutsErr = nil
			return m, m.saveCurrentLayout(name)
		case tea.KeyBackspace:
			if r := []rune(m.layoutName); len(r) > 0 {
				m.layoutName = string(r[:len(r)-1])
			}
			return m, nil
		}
		if k.Text != "" && k.Mod&(tea.ModCtrl|tea.ModAlt) == 0 {
			m.layoutName += k.Text
		} else if k.Mod == 0 && k.Code >= 32 && k.Code < 127 {
			m.layoutName += string(k.Code)
		}
		return m, nil
	}
	switch k.String() {
	case "esc", "q":
		m.layoutsOpen = false
	case "up", "k":
		if m.layoutAt > 0 {
			m.layoutAt--
		}
	case "down", "j":
		if m.layoutAt+1 < len(m.layouts) {
			m.layoutAt++
		}
	case "s":
		m.namingLayout = true
		m.layoutName = ""
		m.layoutsErr = nil
	case "x":
		if layout, ok := m.selectedLayout(); ok {
			m.layoutsErr = nil
			return m, m.deleteLayoutFile(layout.Name)
		}
	case "enter":
		layout, ok := m.selectedLayout()
		if !ok || layout.Err != "" {
			return m, nil
		}
		// Applying replaces the panes; a dirty editor is a refusal, the same
		// as closing a pane or switching machines.
		for _, pane := range m.visiblePanes() {
			if !m.canClose(pane) {
				return m, nil
			}
		}
		m.layoutsOpen = false
		m.closePanes()
		m.loading = true
		m.notice = "Applying layout " + layout.Name + "…"
		return m, m.applyLayoutFile(layout.Name)
	}
	return m, nil
}

// portableFromTree reduces the current layout to what can be rebuilt. A pane
// that is not a terminal has nothing portable to save and is dropped, the same
// way automatic layout persistence treats it.
func portableFromTree(tree *splitNode, terminals map[string]daemon.Terminal, focus *embeddedTerminal) portableLayout {
	layout := portableLayout{Version: 1, Focus: -1}
	index := 0
	var walk func(*splitNode) *portableNode
	walk = func(node *splitNode) *portableNode {
		if node == nil {
			return nil
		}
		if node.pane != nil {
			pane := node.pane
			if pane.terminalID == "" || pane.editor != nil || pane.review != nil {
				return nil
			}
			saved := &portablePane{Terminal: pane.terminalID, Label: pane.title}
			if terminal, ok := terminals[pane.terminalID]; ok {
				saved.Command = append([]string(nil), terminal.Command...)
				saved.Directory = terminal.Directory
			}
			if pane == focus {
				layout.Focus = index
			}
			index++
			return &portableNode{Pane: saved}
		}
		first := walk(node.first)
		second := walk(node.second)
		switch {
		case first == nil:
			return second
		case second == nil:
			return first
		}
		return &portableNode{Stacked: node.stacked, Ratio: node.ratio, First: first, Second: second}
	}
	layout.Root = walk(tree)
	return layout
}

func writePortableLayout(path string, layout portableLayout) error {
	encoded, err := json.MarshalIndent(layout, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	// Written beside the target and renamed, so a crash mid-write cannot
	// leave a torn layout behind.
	temp, err := os.CreateTemp(filepath.Dir(path), "layout-*.json")
	if err != nil {
		return err
	}
	name := temp.Name()
	if _, err := temp.Write(encoded); err != nil {
		_ = temp.Close()
		_ = os.Remove(name)
		return err
	}
	if err := temp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	_ = os.Chmod(name, 0o600)
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}

func readPortableLayout(path string) (portableLayout, error) {
	encoded, err := os.ReadFile(path)
	if err != nil {
		return portableLayout{}, err
	}
	var layout portableLayout
	if err := json.Unmarshal(encoded, &layout); err != nil {
		return portableLayout{}, fmt.Errorf("decode layout %s: %w", filepath.Base(path), err)
	}
	if layout.Root == nil {
		return portableLayout{}, fmt.Errorf("layout %s has no panes", filepath.Base(path))
	}
	return layout, nil
}

// layoutName rejects anything that would climb out of the layouts directory.
// The name arrives from a prompt, but a name that is a path is a mistake worth
// naming rather than silently sanitizing.
func layoutName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("a layout needs a name")
	}
	if strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
		return "", fmt.Errorf("a layout name cannot be a path")
	}
	return name, nil
}

// listLayoutFiles reads the layouts directory. One unreadable file must not
// hide the rest: it is listed with the reason in place of its pane count.
func listLayoutFiles(directory string) ([]layoutSummary, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read layouts: %w", err)
	}
	layouts := []layoutSummary{}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".json")
		layout, err := readPortableLayout(filepath.Join(directory, entry.Name()))
		if err != nil {
			layouts = append(layouts, layoutSummary{Name: name, Err: err.Error()})
			continue
		}
		layouts = append(layouts, layoutSummary{Name: name, Panes: countPortablePanes(layout.Root)})
	}
	slices.SortFunc(layouts, func(left, right layoutSummary) int { return strings.Compare(left.Name, right.Name) })
	return layouts, nil
}

func countPortablePanes(node *portableNode) int {
	if node == nil {
		return 0
	}
	if node.Pane != nil {
		return 1
	}
	return countPortablePanes(node.First) + countPortablePanes(node.Second)
}

// applyPortableLayout resolves every saved leaf against the machine: the same
// terminal if it is still running, a running terminal with the same command,
// or a new one started from the saved command. Leaves that fail are dropped,
// and the tree is rebuilt from what actually attached, exactly like the
// automatic restore.
func applyPortableLayout(ctx context.Context, client *ipc.Client, machineID, workspaceID string,
	saved portableLayout, running []daemon.Terminal, columns, rows int) tea.Msg {

	byID := make(map[string]daemon.Terminal, len(running))
	for _, terminal := range running {
		if terminal.State == "running" {
			byID[terminal.ID] = terminal
		}
	}
	used := map[string]bool{}
	var panes []*embeddedTerminal

	var walk func(*portableNode)
	walk = func(node *portableNode) {
		if node == nil {
			return
		}
		if node.Pane == nil {
			walk(node.First)
			walk(node.Second)
			return
		}
		pane := node.Pane
		terminalID := ""
		if pane.Terminal != "" && !used[pane.Terminal] {
			if _, ok := byID[pane.Terminal]; ok {
				terminalID = pane.Terminal
			}
		}
		if terminalID == "" {
			for _, terminal := range running {
				if terminal.State == "running" && !used[terminal.ID] && len(terminal.Command) > 0 &&
					slices.Equal(terminal.Command, pane.Command) {
					terminalID = terminal.ID
					break
				}
			}
		}
		if terminalID == "" {
			command := pane.Command
			if len(command) == 0 {
				command = []string{defaultShell()}
			}
			startCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			var started daemon.Terminal
			err := client.Call(startCtx, "terminal.start", map[string]any{
				"workspace_id": workspaceID,
				"command":      command,
				"directory":    pane.Directory,
				"columns":      columns,
				"rows":         rows,
			}, &started)
			cancel()
			if err != nil {
				return
			}
			terminalID = started.ID
		}
		term, err := attachEmbeddedTerminal(ctx, client, terminalID, columns, rows)
		if err != nil {
			return
		}
		if pane.Label != "" {
			term.title = pane.Label
		}
		used[terminalID] = true
		panes = append(panes, term)
	}
	walk(saved.Root)
	if len(panes) == 0 {
		return layoutActionMsg{err: fmt.Errorf("no pane from the layout could be opened"), machineID: machineID}
	}

	next := 0
	var rebuild func(*portableNode) *splitNode
	rebuild = func(node *portableNode) *splitNode {
		if node == nil {
			return nil
		}
		if node.Pane != nil {
			if next >= len(panes) {
				return nil
			}
			pane := panes[next]
			next++
			return &splitNode{pane: pane}
		}
		first := rebuild(node.First)
		second := rebuild(node.Second)
		switch {
		case first == nil:
			return second
		case second == nil:
			return first
		}
		return &splitNode{stacked: node.Stacked, ratio: node.Ratio, first: first, second: second}
	}
	tree := rebuild(saved.Root)
	if tree == nil {
		for _, pane := range panes {
			pane.close()
		}
		return layoutActionMsg{err: fmt.Errorf("the layout could not be rebuilt"), machineID: machineID}
	}
	leaves := tree.leaves(nil)
	focus := leaves[len(leaves)-1]
	if saved.Focus >= 0 && saved.Focus < len(leaves) {
		focus = leaves[saved.Focus]
	}
	return layoutRestoredMsg{tree: tree, focus: focus, panes: panes, machineID: machineID}
}
