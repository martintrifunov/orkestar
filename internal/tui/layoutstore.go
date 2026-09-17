package tui

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	tea "charm.land/bubbletea/v2"

	"github.com/martintrifunov/orkestar/internal/daemon"
	"github.com/martintrifunov/orkestar/internal/ipc"
)

// The client owns the pane layout, so it is the client that remembers it. A
// saved layout is the split tree and which terminal each leaf attaches to;
// editor and review panes are left out, since their content lives only in
// memory and would come back blank.

type persistedLayout struct {
	Focus string         `json:"focus,omitempty"`
	Root  *persistedNode `json:"root,omitempty"`
}

type persistedNode struct {
	Terminal string         `json:"terminal,omitempty"`
	Stacked  bool           `json:"stacked,omitempty"`
	Ratio    float64        `json:"ratio,omitempty"`
	First    *persistedNode `json:"first,omitempty"`
	Second   *persistedNode `json:"second,omitempty"`
}

// persistedTree reduces a layout to what can be rebuilt: terminal leaves, and
// the splits above them. A leaf that is not a terminal, or an empty subtree,
// becomes nil and is dropped.
func persistedTree(node *splitNode) *persistedNode {
	if node == nil {
		return nil
	}
	if node.pane != nil {
		if node.pane.terminalID == "" || node.pane.editor != nil || node.pane.review != nil {
			return nil
		}
		return &persistedNode{Terminal: node.pane.terminalID}
	}
	first := persistedTree(node.first)
	second := persistedTree(node.second)
	switch {
	case first == nil:
		return second
	case second == nil:
		return first
	}
	return &persistedNode{Stacked: node.stacked, Ratio: node.ratio, First: first, Second: second}
}

// rebuiltTree restores a saved tree against the terminals that actually
// attached. A leaf whose terminal did not come back is dropped, and a split
// with no children left disappears with it. A terminal named twice is kept
// once: two leaves would hold the same attachment, which closes under one of
// them.
func rebuiltTree(node *persistedNode, panes map[string]*embeddedTerminal) *splitNode {
	return rebuiltTreeSeen(node, panes, map[string]bool{})
}

func rebuiltTreeSeen(node *persistedNode, panes map[string]*embeddedTerminal, seen map[string]bool) *splitNode {
	if node == nil {
		return nil
	}
	if node.Terminal != "" {
		if seen[node.Terminal] {
			return nil
		}
		pane := panes[node.Terminal]
		if pane == nil {
			return nil
		}
		seen[node.Terminal] = true
		return &splitNode{pane: pane}
	}
	first := rebuiltTreeSeen(node.First, panes, seen)
	second := rebuiltTreeSeen(node.Second, panes, seen)
	switch {
	case first == nil:
		return second
	case second == nil:
		return first
	}
	return &splitNode{stacked: node.Stacked, ratio: node.Ratio, first: first, second: second}
}

func saveLayout(path string, tree *splitNode, focus string) {
	if path == "" {
		return
	}
	root := persistedTree(tree)
	if root == nil {
		_ = os.Remove(path)
		return
	}
	encoded, err := json.Marshal(persistedLayout{Focus: focus, Root: root})
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	// Write beside the target and rename, so a crash mid-write cannot
	// leave a torn layout that loads as lost.
	temp, err := os.CreateTemp(filepath.Dir(path), "layout-*.json")
	if err != nil {
		return
	}
	name := temp.Name()
	if _, err := temp.Write(encoded); err != nil {
		_ = temp.Close()
		_ = os.Remove(name)
		return
	}
	if err := temp.Close(); err != nil {
		_ = os.Remove(name)
		return
	}
	_ = os.Chmod(name, 0o600)
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
	}
}

func loadLayout(path string) (persistedLayout, bool) {
	if path == "" {
		return persistedLayout{}, false
	}
	encoded, err := os.ReadFile(path)
	if err != nil {
		return persistedLayout{}, false
	}
	var layout persistedLayout
	if json.Unmarshal(encoded, &layout) != nil || layout.Root == nil {
		return persistedLayout{}, false
	}
	return layout, true
}

// persistLayout writes the current arrangement. It runs as the layout changes
// and once more on the way out, when the focused pane is settled.
func (m Model) persistLayout() {
	if m.layoutPath == "" {
		return
	}
	focus := ""
	if m.embedded != nil {
		focus = m.embedded.terminalID
	}
	saveLayout(m.layoutPath, m.tree(), focus)
}

// layoutRestoredMsg carries the panes that came back and the tree they form.
type layoutRestoredMsg struct {
	tree  *splitNode
	focus *embeddedTerminal
	panes []*embeddedTerminal
	// machineID is the machine the panes were attached to, so a restore that
	// lands after a switch is not installed into the wrong board.
	machineID string
}

// runningTerminals is the set of terminal IDs a restored layout may attach to.
func runningTerminals(snapshot daemon.Snapshot) map[string]bool {
	running := make(map[string]bool, len(snapshot.Terminals))
	for _, terminal := range snapshot.Terminals {
		if terminal.State == "running" {
			running[terminal.ID] = true
		}
	}
	return running
}

// restoreLayout attaches to each saved terminal that is still running and
// rebuilds the tree from the ones that answer. Attachment is sequential and
// synchronous so the first pane to attach is the input controller, and the
// tree is built from what actually succeeded rather than what was saved.
func restoreLayout(ctx context.Context, client *ipc.Client, machineID string, saved persistedLayout, running map[string]bool) tea.Cmd {
	if ctx == nil {
		ctx = context.Background()
	}
	return func() tea.Msg {
		panes := map[string]*embeddedTerminal{}
		var opened []*embeddedTerminal
		var walk func(*persistedNode)
		walk = func(node *persistedNode) {
			if node == nil {
				return
			}
			if node.Terminal != "" {
				if !running[node.Terminal] {
					return
				}
				if _, done := panes[node.Terminal]; done {
					return
				}
				term, err := attachEmbeddedTerminal(ctx, client, node.Terminal, 80, 24)
				if err != nil {
					return
				}
				panes[node.Terminal] = term
				opened = append(opened, term)
				return
			}
			walk(node.First)
			walk(node.Second)
		}
		walk(saved.Root)
		if len(opened) == 0 {
			return layoutRestoredMsg{}
		}
		tree := rebuiltTree(saved.Root, panes)
		focus := panes[saved.Focus]
		if focus == nil {
			if leaves := tree.leaves(nil); len(leaves) > 0 {
				focus = leaves[len(leaves)-1]
			}
		}
		return layoutRestoredMsg{tree: tree, focus: focus, panes: opened, machineID: machineID}
	}
}
