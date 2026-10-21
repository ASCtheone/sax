package session

import "fmt"

// Session holds all tabs and global state.
type Session struct {
	Tabs      []*Tab
	ActiveTab int
	Width     int
	Height    int
}

// NewSession creates a new session with one tab.
func NewSession(cols, rows int, workDir string) (*Session, error) {
	tab, err := NewTab(cols, rows, workDir)
	if err != nil {
		return nil, err
	}

	return &Session{
		Tabs:      []*Tab{tab},
		ActiveTab: 0,
		Width:     cols,
		Height:    rows,
	}, nil
}

// NewSessionWithCommand creates a new session running a specific command.
func NewSessionWithCommand(cols, rows int, name string, args []string, workDir string) (*Session, error) {
	tab, err := NewTabWithCommand(cols, rows, name, args, workDir)
	if err != nil {
		return nil, err
	}

	return &Session{
		Tabs:      []*Tab{tab},
		ActiveTab: 0,
		Width:     cols,
		Height:    rows,
	}, nil
}

// CurrentTab returns the active tab.
func (s *Session) CurrentTab() *Tab {
	if s.ActiveTab >= 0 && s.ActiveTab < len(s.Tabs) {
		return s.Tabs[s.ActiveTab]
	}
	return nil
}

// ActivePane returns the active pane of the current tab.
func (s *Session) ActivePane() *Pane {
	tab := s.CurrentTab()
	if tab == nil {
		return nil
	}
	return tab.ActivePaneObj()
}

// AddTab creates and adds a new tab.
func (s *Session) AddTab() (*Tab, error) {
	// Calculate pane area (minus tab bar and status bar)
	cols, rows := s.PaneArea()
	tab, err := NewTab(cols, rows, "")
	if err != nil {
		return nil, err
	}
	s.Tabs = append(s.Tabs, tab)
	s.ActiveTab = len(s.Tabs) - 1
	return tab, nil
}

// AddCommandTab adds a named tab running a command (with extra environment
// for its process), keeping the current tab focused.
func (s *Session) AddCommandTab(name, cmd string, args []string, workDir string, env []string) (*Tab, error) {
	cols, rows := s.PaneArea()
	tab, err := NewTabWithCommandEnv(cols, rows, cmd, args, workDir, env)
	if err != nil {
		return nil, err
	}
	tab.Name = name
	s.Tabs = append(s.Tabs, tab)
	return tab, nil
}

// ReplaceTab starts a named command tab in old's place (same position, same
// focus) and returns it. old is taken out of the session but left running;
// the caller stops and closes it.
func (s *Session) ReplaceTab(old *Tab, name, cmd string, args []string, workDir string, env []string) (*Tab, error) {
	idx := -1
	for i, t := range s.Tabs {
		if t == old {
			idx = i
		}
	}
	if idx < 0 {
		return nil, fmt.Errorf("tab %s is not in the session", old.Name)
	}
	cols, rows := s.PaneArea()
	tab, err := NewTabWithCommandEnv(cols, rows, cmd, args, workDir, env)
	if err != nil {
		return nil, err
	}
	tab.Name = name
	tabs := append([]*Tab{}, s.Tabs...)
	tabs[idx] = tab
	s.Tabs = tabs
	return tab, nil
}

// NewSessionWithTab creates a session whose first tab is named and runs a
// command with extra environment.
func NewSessionWithTab(cols, rows int, tabName, cmd string, args []string, workDir string, env []string) (*Session, error) {
	tab, err := NewTabWithCommandEnv(cols, rows, cmd, args, workDir, env)
	if err != nil {
		return nil, err
	}
	tab.Name = tabName
	return &Session{Tabs: []*Tab{tab}, Width: cols, Height: rows}, nil
}

// TabByName returns the tab with the given name, or nil.
func (s *Session) TabByName(name string) *Tab {
	for _, t := range s.Tabs {
		if t.Name == name {
			return t
		}
	}
	return nil
}

// RemoveTab removes the tab at the given index.
func (s *Session) RemoveTab(idx int) {
	if idx < 0 || idx >= len(s.Tabs) {
		return
	}
	s.Tabs[idx].Close()
	s.Tabs = append(s.Tabs[:idx], s.Tabs[idx+1:]...)

	if s.ActiveTab >= len(s.Tabs) {
		s.ActiveTab = len(s.Tabs) - 1
	}
	if s.ActiveTab < 0 {
		s.ActiveTab = 0
	}
}

// NextTab switches to the next tab.
func (s *Session) NextTab() {
	if len(s.Tabs) > 1 {
		s.ActiveTab = (s.ActiveTab + 1) % len(s.Tabs)
	}
}

// PrevTab switches to the previous tab.
func (s *Session) PrevTab() {
	if len(s.Tabs) > 1 {
		s.ActiveTab = (s.ActiveTab - 1 + len(s.Tabs)) % len(s.Tabs)
	}
}

// GoToTab switches to the tab at the given index.
func (s *Session) GoToTab(idx int) {
	if idx >= 0 && idx < len(s.Tabs) {
		s.ActiveTab = idx
	}
}

// ShowTabBar returns true if the tab bar should be visible (2+ tabs).
func (s *Session) ShowTabBar() bool {
	return len(s.Tabs) > 1
}

// PaneArea returns the dimensions available for panes.
// Subtracts status bar always, tab bar only when multiple tabs exist.
func (s *Session) PaneArea() (int, int) {
	chrome := 1 // status bar
	if s.ShowTabBar() {
		chrome++ // tab bar
	}
	h := s.Height - chrome
	if h < 1 {
		h = 1
	}
	w := s.Width
	if w < 1 {
		w = 1
	}
	return w, h
}

// Resize updates the session dimensions and resizes all tabs.
func (s *Session) Resize(w, h int) {
	s.Width = w
	s.Height = h
	cols, rows := s.PaneArea()
	for _, tab := range s.Tabs {
		tab.ResizePanes(Rect{X: 0, Y: 0, W: cols, H: rows})
	}
}

// AllPanes returns all panes across all tabs.
func (s *Session) AllPanes() []*Pane {
	var panes []*Pane
	for _, tab := range s.Tabs {
		for _, p := range tab.Panes {
			panes = append(panes, p)
		}
	}
	return panes
}

// FindPaneTab returns the tab index containing the given pane ID.
func (s *Session) FindPaneTab(paneID string) int {
	for i, tab := range s.Tabs {
		if _, ok := tab.Panes[paneID]; ok {
			return i
		}
	}
	return -1
}

// Close closes all tabs and their panes.
func (s *Session) Close() {
	for _, tab := range s.Tabs {
		tab.Close()
	}
}

// TabNames returns the names of all tabs for display.
func (s *Session) TabNames() []string {
	names := make([]string, len(s.Tabs))
	for i, tab := range s.Tabs {
		names[i] = fmt.Sprintf("%d:%s", i+1, tab.Name)
	}
	return names
}
