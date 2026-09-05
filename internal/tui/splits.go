package tui

import (
	"math"

	"charm.land/lipgloss/v2"
)

// splitNode is one node of the pane layout tree. A leaf holds a pane; an
// internal node divides its rectangle between two children, side by side or
// stacked. The tree is authoritative for geometry. Focus cycling and the
// "n panes" label use the in-order sequence of leaves.
type splitNode struct {
	pane          *embeddedTerminal
	stacked       bool
	first, second *splitNode
	// ratio is the share of the rectangle the first child receives. Zero means
	// an even split, so a tree built before any resize needs no migration.
	ratio float64
}

// split returns the first child's size along the divided axis, honouring the
// ratio while keeping both sides large enough to use.
func (n *splitNode) split(total, minimum int) int {
	ratio := n.ratio
	if ratio <= 0 {
		ratio = 0.5
	}
	size := int(math.Round(float64(total) * ratio))
	if total >= 2*minimum {
		size = max(minimum, min(total-minimum, size))
	}
	return size
}

// setRatio moves the divider, keeping it inside the range where both children
// stay usable.
func (n *splitNode) setRatio(ratio float64) {
	n.ratio = math.Max(0.1, math.Min(0.9, ratio))
}

// divider is the movable boundary between an internal node's two children,
// carried with the rectangle it divides so a drag can be turned into a ratio.
type divider struct {
	node                *splitNode
	vertical            bool
	x, y, width, height int
}

// at returns the screen position of the divider line itself.
func (d divider) at() int {
	if d.vertical {
		return d.x + d.node.split(d.width, minPaneWidth)
	}
	return d.y + d.node.split(d.height, minPaneHeight)
}

// dividers collects every movable boundary, laid out over the same rectangles
// the panes are drawn in.
func (n *splitNode) dividers(x, y, width, height int, out []divider) []divider {
	if n == nil || n.pane != nil {
		return out
	}
	out = append(out, divider{n, !n.stacked, x, y, width, height})
	if n.stacked {
		top := n.split(height, minPaneHeight)
		out = n.first.dividers(x, y, width, top, out)
		return n.second.dividers(x, y+top, width, height-top, out)
	}
	left := n.split(width, minPaneWidth)
	out = n.first.dividers(x, y, left, height, out)
	return n.second.dividers(x+left, y, width-left, height, out)
}

// ancestor finds the nearest enclosing split of the given orientation, which
// is the divider a resize key should move.
func (n *splitNode) ancestor(pane *embeddedTerminal, stacked bool) *splitNode {
	if n == nil || n.pane != nil {
		return nil
	}
	for _, child := range []*splitNode{n.first, n.second} {
		if found := child.ancestor(pane, stacked); found != nil {
			return found
		}
	}
	if n.stacked == stacked && len(n.leaves(nil)) > 0 {
		for _, leaf := range n.leaves(nil) {
			if leaf == pane {
				return n
			}
		}
	}
	return nil
}

// splitRequest remembers where a pane launched by Ctrl+b v/s must land, so a
// focus change while the daemon starts the shell cannot misplace it.
type splitRequest struct {
	target  *embeddedTerminal
	stacked bool
}

func (n *splitNode) leaves(out []*embeddedTerminal) []*embeddedTerminal {
	if n == nil {
		return out
	}
	if n.pane != nil {
		return append(out, n.pane)
	}
	return n.second.leaves(n.first.leaves(out))
}

// find returns the leaf holding p and that leaf's parent (nil for the root).
func (n *splitNode) find(p *embeddedTerminal, parent *splitNode) (leaf, par *splitNode) {
	if n == nil || p == nil {
		return nil, nil
	}
	if n.pane != nil {
		if n.pane == p {
			return n, parent
		}
		return nil, nil
	}
	if leaf, par := n.first.find(p, n); leaf != nil {
		return leaf, par
	}
	return n.second.find(p, n)
}

// insert places p beside (or, when stacked, below) the leaf holding target.
// The target leaf becomes an internal node in place, so every other pane keeps
// its position. Without a matching target the whole layout is split instead.
func (n *splitNode) insert(target, p *embeddedTerminal, stacked bool) *splitNode {
	if n == nil {
		return &splitNode{pane: p}
	}
	leaf, _ := n.find(target, nil)
	if leaf == nil {
		return &splitNode{stacked: stacked, first: n, second: &splitNode{pane: p}}
	}
	leaf.first = &splitNode{pane: leaf.pane}
	leaf.second = &splitNode{pane: p}
	leaf.pane = nil
	leaf.stacked = stacked
	return n
}

// sibling returns the panes that inherit p's space when p is removed, in
// layout order. It is empty when p is the only pane or is not in the tree.
func (n *splitNode) sibling(p *embeddedTerminal) []*embeddedTerminal {
	leaf, parent := n.find(p, nil)
	if leaf == nil || parent == nil {
		return nil
	}
	other := parent.first
	if other == leaf {
		other = parent.second
	}
	return other.leaves(nil)
}

// remove deletes the leaf holding p. Its parent collapses into the sibling so
// the sibling subtree takes over the parent's rectangle.
func (n *splitNode) remove(p *embeddedTerminal) *splitNode {
	if n == nil || p == nil {
		return n
	}
	if n.pane == p {
		return nil
	}
	leaf, parent := n.find(p, nil)
	if leaf == nil {
		return n
	}
	other := parent.first
	if other == leaf {
		other = parent.second
	}
	*parent = *other
	return n
}

// rects tiles the rectangle at (x, y) with the given width and height over the
// leaves. Children split their parent in half; the second child takes the
// remainder so the rectangles always cover the parent exactly.
func (n *splitNode) rects(x, y, width, height int, out []paneRect) []paneRect {
	if n == nil {
		return out
	}
	if n.pane != nil {
		return append(out, paneRect{n.pane, x, y, width, height})
	}
	if n.stacked {
		top := n.split(height, minPaneHeight)
		out = n.first.rects(x, y, width, top, out)
		return n.second.rects(x, y+top, width, height-top, out)
	}
	left := n.split(width, minPaneWidth)
	out = n.first.rects(x, y, left, height, out)
	return n.second.rects(x+left, y, width-left, height, out)
}

// render joins already rendered pane boxes along the tree. Sibling boxes share
// a height (side by side) or a width (stacked) because they were sized from the
// same parent rectangle, so the joins produce one exact block.
func (n *splitNode) render(boxes map[*embeddedTerminal]string) string {
	if n.pane != nil {
		return boxes[n.pane]
	}
	if n.stacked {
		return lipgloss.JoinVertical(lipgloss.Left, n.first.render(boxes), n.second.render(boxes))
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, n.first.render(boxes), n.second.render(boxes))
}

// minimum pane box size, including border, below which splits are hidden and
// only the focused pane is shown.
const (
	minPaneWidth  = 24
	minPaneHeight = 7
)
