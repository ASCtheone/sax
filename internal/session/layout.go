package session

// SplitDir represents the direction of a split.
type SplitDir int

const (
	SplitHorizontal SplitDir = iota // top/bottom
	SplitVertical                   // left/right
)

// LayoutNode is a binary tree node representing the pane layout.
// Leaf nodes hold a PaneID; internal nodes define a split.
type LayoutNode struct {
	// For leaf nodes
	PaneID string
	IsLeaf bool

	// For internal nodes
	Dir   SplitDir
	Ratio float64 // 0.0-1.0 — fraction allocated to First child
	First *LayoutNode
	Sec   *LayoutNode
}

// NewLeaf creates a leaf node for a pane.
func NewLeaf(paneID string) *LayoutNode {
	return &LayoutNode{
		PaneID: paneID,
		IsLeaf: true,
	}
}

// Rect represents a rectangular area on screen.
type Rect struct {
	X, Y, W, H int
}

// Arrange computes the position and size of each pane leaf.
func (n *LayoutNode) Arrange(area Rect) map[string]Rect {
	result := make(map[string]Rect)
	n.arrange(area, result)
	return result
}

func (n *LayoutNode) arrange(area Rect, result map[string]Rect) {
	if n.IsLeaf {
		result[n.PaneID] = area
		return
	}

	switch n.Dir {
	case SplitVertical:
		// left | right
		leftW := int(float64(area.W) * n.Ratio)
		if leftW < 1 {
			leftW = 1
		}
		rightW := area.W - leftW - 1 // 1 for border
		if rightW < 1 {
			rightW = 1
		}
		n.First.arrange(Rect{X: area.X, Y: area.Y, W: leftW, H: area.H}, result)
		n.Sec.arrange(Rect{X: area.X + leftW + 1, Y: area.Y, W: rightW, H: area.H}, result)

	case SplitHorizontal:
		// top / bottom
		topH := int(float64(area.H) * n.Ratio)
		if topH < 1 {
			topH = 1
		}
		botH := area.H - topH - 1 // 1 for border
		if botH < 1 {
			botH = 1
		}
		n.First.arrange(Rect{X: area.X, Y: area.Y, W: area.W, H: topH}, result)
		n.Sec.arrange(Rect{X: area.X, Y: area.Y + topH + 1, W: area.W, H: botH}, result)
	}
}

// Split replaces the leaf with the given paneID with an internal node,
// keeping the original pane as the first child and creating a new pane as the second.
func (n *LayoutNode) Split(paneID, newPaneID string, dir SplitDir) bool {
	if n.IsLeaf {
		if n.PaneID == paneID {
			// Transform this leaf into a split node
			n.IsLeaf = false
			n.Dir = dir
			n.Ratio = 0.5
			n.First = NewLeaf(paneID)
			n.Sec = NewLeaf(newPaneID)
			n.PaneID = ""
			return true
		}
		return false
	}
	if n.First.Split(paneID, newPaneID, dir) {
		return true
	}
	return n.Sec.Split(paneID, newPaneID, dir)
}

// Remove removes the leaf with the given paneID from the tree.
// Returns true if the removal was successful. The parent is replaced
// by the sibling of the removed node.
func (n *LayoutNode) Remove(paneID string) bool {
	if n.IsLeaf {
		return false
	}

	// Check if first child is the target leaf
	if n.First.IsLeaf && n.First.PaneID == paneID {
		*n = *n.Sec
		return true
	}
	// Check if second child is the target leaf
	if n.Sec.IsLeaf && n.Sec.PaneID == paneID {
		*n = *n.First
		return true
	}

	// Recurse
	if n.First.Remove(paneID) {
		return true
	}
	return n.Sec.Remove(paneID)
}

// PaneIDs returns all pane IDs in the layout tree (in-order traversal).
func (n *LayoutNode) PaneIDs() []string {
	if n.IsLeaf {
		return []string{n.PaneID}
	}
	ids := n.First.PaneIDs()
	ids = append(ids, n.Sec.PaneIDs()...)
	return ids
}

// PaneCount returns the number of panes in the layout.
func (n *LayoutNode) PaneCount() int {
	if n.IsLeaf {
		return 1
	}
	return n.First.PaneCount() + n.Sec.PaneCount()
}

// FindNeighbor finds the best pane to navigate to from the given pane in the given direction.
func (n *LayoutNode) FindNeighbor(paneID string, dir SplitDir, second bool, rects map[string]Rect) string {
	ids := n.PaneIDs()
	if len(ids) <= 1 {
		return paneID
	}

	current, ok := rects[paneID]
	if !ok {
		return paneID
	}

	bestID := paneID
	bestDist := int(^uint(0) >> 1) // max int

	for _, id := range ids {
		if id == paneID {
			continue
		}
		r := rects[id]

		switch {
		case dir == SplitVertical && !second: // left
			if r.X+r.W <= current.X {
				dist := current.X - (r.X + r.W)
				if dist < bestDist {
					bestDist = dist
					bestID = id
				}
			}
		case dir == SplitVertical && second: // right
			if r.X >= current.X+current.W {
				dist := r.X - (current.X + current.W)
				if dist < bestDist {
					bestDist = dist
					bestID = id
				}
			}
		case dir == SplitHorizontal && !second: // up
			if r.Y+r.H <= current.Y {
				dist := current.Y - (r.Y + r.H)
				if dist < bestDist {
					bestDist = dist
					bestID = id
				}
			}
		case dir == SplitHorizontal && second: // down
			if r.Y >= current.Y+current.H {
				dist := r.Y - (current.Y + current.H)
				if dist < bestDist {
					bestDist = dist
					bestID = id
				}
			}
		}
	}

	return bestID
}
