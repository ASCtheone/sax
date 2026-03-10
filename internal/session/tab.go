package session

import (
	"fmt"
	"sync/atomic"
)

var tabCounter atomic.Uint64

func nextTabID() string {
	return fmt.Sprintf("tab-%d", tabCounter.Add(1))
}

// Tab represents a tab containing a layout of panes.
type Tab struct {
	ID         string
	Name       string
	Layout     *LayoutNode
	Panes      map[string]*Pane
	ActivePane string
	Zoomed     bool
	ZoomedPane string
}

// NewTab creates a new tab with a single pane.
func NewTab(cols, rows int, workDir string) (*Tab, error) {
	pane, err := NewPane(cols, rows, workDir)
	if err != nil {
		return nil, err
	}

	id := nextTabID()
	num := tabCounter.Load()

	return &Tab{
		ID:         id,
		Name:       fmt.Sprintf("tab %d", num),
		Layout:     NewLeaf(pane.ID),
		Panes:      map[string]*Pane{pane.ID: pane},
		ActivePane: pane.ID,
	}, nil
}

// NewTabWithCommand creates a new tab running a specific command.
func NewTabWithCommand(cols, rows int, cmdName string, args []string, workDir string) (*Tab, error) {
	pane, err := NewPaneWithCommand(cols, rows, cmdName, args, workDir)
	if err != nil {
		return nil, err
	}

	id := nextTabID()

	return &Tab{
		ID:         id,
		Name:       cmdName,
		Layout:     NewLeaf(pane.ID),
		Panes:      map[string]*Pane{pane.ID: pane},
		ActivePane: pane.ID,
	}, nil
}

// ActivePaneObj returns the currently active Pane object.
func (t *Tab) ActivePaneObj() *Pane {
	if p, ok := t.Panes[t.ActivePane]; ok {
		return p
	}
	return nil
}

// SplitActive splits the active pane in the given direction.
func (t *Tab) SplitActive(dir SplitDir, cols, rows int) (*Pane, error) {
	if t.Layout.PaneCount() >= 4 {
		return nil, fmt.Errorf("maximum 4 panes per tab")
	}

	newPane, err := NewPane(cols, rows, "")
	if err != nil {
		return nil, err
	}

	if !t.Layout.Split(t.ActivePane, newPane.ID, dir) {
		newPane.Close()
		return nil, fmt.Errorf("failed to split pane")
	}

	t.Panes[newPane.ID] = newPane
	t.ActivePane = newPane.ID
	return newPane, nil
}

// RemovePane removes a pane from the tab. Returns true if the tab still has panes.
func (t *Tab) RemovePane(paneID string) bool {
	pane, ok := t.Panes[paneID]
	if !ok {
		return len(t.Panes) > 0
	}

	pane.Close()
	delete(t.Panes, paneID)

	if len(t.Panes) == 0 {
		return false
	}

	// Remove from layout
	if t.Layout.IsLeaf && t.Layout.PaneID == paneID {
		// Last pane — shouldn't happen since we checked above
		return false
	}
	t.Layout.Remove(paneID)

	// If the active pane was removed, switch to the first available
	if t.ActivePane == paneID {
		ids := t.Layout.PaneIDs()
		if len(ids) > 0 {
			t.ActivePane = ids[0]
		}
	}

	// Cancel zoom if zoomed pane was removed
	if t.Zoomed && t.ZoomedPane == paneID {
		t.Zoomed = false
		t.ZoomedPane = ""
	}

	return true
}

// Close closes all panes in the tab.
func (t *Tab) Close() {
	for _, p := range t.Panes {
		p.Close()
	}
}

// ResizePanes resizes all panes according to the current layout.
func (t *Tab) ResizePanes(area Rect) {
	rects := t.Layout.Arrange(area)
	for id, r := range rects {
		if p, ok := t.Panes[id]; ok {
			p.Resize(r.W, r.H)
		}
	}
}
