package app

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/asc/sax/internal/porttracker"
	"github.com/asc/sax/internal/session"
	"github.com/asc/sax/internal/statusbar"
	"github.com/asc/sax/internal/tabbar"
	"github.com/asc/sax/internal/theme"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"
)

// Model is the root bubbletea model for sax.
type Model struct {
	session  *session.Session
	mode     Mode
	showHelp bool
	ports    []PortInfo
	width    int
	height   int
	ready    bool
	program  *tea.Program
	tracker  *porttracker.Tracker
}

// New creates a new Model.
func New() *Model {
	return &Model{}
}

// SetProgram stores the program reference for sending messages from goroutines.
func (m *Model) SetProgram(p *tea.Program) {
	m.program = p
}

// Init initializes the model. It detects terminal size as a fallback
// in case WindowSizeMsg is delayed.
func (m *Model) Init() tea.Cmd {
	// Try to detect terminal size immediately as a fallback
	return func() tea.Msg {
		w, h, err := term.GetSize(int(os.Stdout.Fd()))
		if err != nil || w == 0 || h == 0 {
			w, h = 80, 24
		}
		return tea.WindowSizeMsg{Width: w, Height: h}
	}
}

// Update handles messages.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return m.handleResize(msg)

	case tea.KeyMsg:
		return m.handleKey(msg)

	case tea.MouseMsg:
		return m.handleMouse(msg)

	case PtyOutputMsg:
		return m, nil

	case PtyExitMsg:
		return m.handlePtyExit(msg)

	case PortUpdateMsg:
		m.ports = msg.Ports
		return m, nil

	case ClearPrefixMsg:
		if m.mode == ModePrefix {
			m.mode = ModeNormal
		}
		return m, nil
	}

	return m, nil
}

// View renders the full screen.
func (m *Model) View() string {
	if !m.ready {
		return "Starting sax..."
	}

	if m.showHelp {
		return m.renderHelp()
	}

	var lines []string

	// Tab bar
	tabNames := m.session.TabNames()
	lines = append(lines, tabbar.Render(tabNames, m.session.ActiveTab, m.width))

	// Pane area
	tab := m.session.CurrentTab()
	if tab != nil {
		cols, rows := m.session.PaneArea()
		paneArea := m.renderPanes(tab, cols, rows)
		lines = append(lines, paneArea)
	}

	// Status bar
	var modeStatus statusbar.Mode
	if m.mode == ModePrefix {
		modeStatus = statusbar.ModePrefix
	}
	var sPorts []statusbar.PortInfo
	for _, p := range m.ports {
		sPorts = append(sPorts, statusbar.PortInfo{Port: p.Port, Process: p.Process})
	}

	paneInfo := ""
	if tab != nil {
		paneCount := tab.Layout.PaneCount()
		if paneCount > 1 {
			paneInfo = fmt.Sprintf("[%d panes]", paneCount)
		}
	}
	lines = append(lines, statusbar.Render(modeStatus, sPorts, paneInfo, m.width))

	return strings.Join(lines, "\n")
}

// handleResize processes window resize events.
func (m *Model) handleResize(msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
	m.width = msg.Width
	m.height = msg.Height

	if !m.ready {
		sess, err := session.NewSession(msg.Width, msg.Height-2, "")
		if err != nil {
			return m, tea.Quit
		}
		m.session = sess
		m.ready = true

		// Start port tracker
		m.tracker = porttracker.New(func(ports []porttracker.ListeningPort) {
			if m.program != nil {
				var pi []PortInfo
				for _, p := range ports {
					pi = append(pi, PortInfo{Port: p.Port, PID: int(p.PID), Process: p.Process})
				}
				m.program.Send(PortUpdateMsg{Ports: pi})
			}
		})
		m.tracker.Start()

		var cmds []tea.Cmd
		for _, pane := range sess.AllPanes() {
			cmds = append(cmds, m.readPty(pane))
		}
		return m, tea.Batch(cmds...)
	}

	m.session.Resize(msg.Width, msg.Height)
	return m, nil
}

// handleKey processes keyboard input.
func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// Ctrl+Q to quit
	if key == "ctrl+q" {
		if m.tracker != nil {
			m.tracker.Stop()
		}
		m.session.Close()
		return m, tea.Quit
	}

	// Prefix mode activation
	if key == PrefixKey {
		if m.mode == ModeNormal {
			m.mode = ModePrefix
			return m, tea.Tick(2*time.Second, func(time.Time) tea.Msg {
				return ClearPrefixMsg{}
			})
		}
		// Double Ctrl+S sends literal Ctrl+S to PTY
		m.mode = ModeNormal
		pane := m.session.ActivePane()
		if pane != nil {
			_, _ = pane.Pty.Write([]byte{0x13})
		}
		return m, nil
	}

	// Prefix mode commands
	if m.mode == ModePrefix {
		m.mode = ModeNormal
		cmd, consumed := m.handlePrefixKey(msg)
		if consumed {
			return m, cmd
		}
	}

	// Normal mode — forward keypress to active PTY
	pane := m.session.ActivePane()
	if pane != nil && !pane.HasExited {
		data := keyToBytes(msg)
		if len(data) > 0 {
			_, _ = pane.Pty.Write(data)
		}
	}

	return m, nil
}

// handleMouse processes mouse events.
func (m *Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if msg.Type != tea.MouseLeft {
		return m, nil
	}

	tab := m.session.CurrentTab()
	if tab == nil {
		return m, nil
	}

	cols, rows := m.session.PaneArea()
	rects := tab.Layout.Arrange(session.Rect{X: 0, Y: 0, W: cols, H: rows})

	clickX := msg.X
	clickY := msg.Y - 1 // subtract tab bar row

	for id, r := range rects {
		if clickX >= r.X && clickX < r.X+r.W && clickY >= r.Y && clickY < r.Y+r.H {
			tab.ActivePane = id
			break
		}
	}

	return m, nil
}

// handlePtyExit handles a pane's PTY process exiting.
func (m *Model) handlePtyExit(msg PtyExitMsg) (tea.Model, tea.Cmd) {
	if m.session == nil {
		return m, nil
	}

	tabIdx := m.session.FindPaneTab(msg.PaneID)
	if tabIdx < 0 {
		return m, nil
	}

	tab := m.session.Tabs[tabIdx]
	alive := tab.RemovePane(msg.PaneID)

	if !alive {
		m.session.RemoveTab(tabIdx)
		if len(m.session.Tabs) == 0 {
			return m, tea.Quit
		}
	}

	return m, nil
}

// renderPanes renders the pane area with borders.
func (m *Model) renderPanes(tab *session.Tab, width, height int) string {
	if tab.Zoomed {
		if p, ok := tab.Panes[tab.ZoomedPane]; ok {
			return p.Render()
		}
	}

	rects := tab.Layout.Arrange(session.Rect{X: 0, Y: 0, W: width, H: height})

	// Single pane — render directly
	if len(rects) == 1 {
		for _, p := range tab.Panes {
			return p.Render()
		}
	}

	// Multiple panes — composite with borders
	paneRenders := make(map[string]string)
	for id := range rects {
		if p, ok := tab.Panes[id]; ok {
			paneRenders[id] = p.Render()
		}
	}

	return m.compositePanes(tab, rects, paneRenders, width, height)
}

// compositePanes assembles pane content with borders into the final pane area.
func (m *Model) compositePanes(tab *session.Tab, rects map[string]session.Rect, renders map[string]string, width, height int) string {
	// Build a character grid
	grid := make([][]rune, height)
	for y := range grid {
		grid[y] = make([]rune, width)
		for x := range grid[y] {
			grid[y][x] = ' '
		}
	}

	// Draw borders between panes
	m.drawNodeBorders(tab.Layout, session.Rect{X: 0, Y: 0, W: width, H: height}, tab.ActivePane, grid)

	// Classify borders as active/inactive using PUA markers
	if ar, ok := rects[tab.ActivePane]; ok {
		theme.ClassifyBorders(grid, ar.X, ar.Y, ar.W, ar.H, width, height)
	}

	// Build the output as string rows
	rows := make([]string, height)
	for y := 0; y < height; y++ {
		rows[y] = string(grid[y])
	}

	// Overlay pane content onto the grid
	for id, r := range rects {
		content, ok := renders[id]
		if !ok {
			continue
		}
		contentLines := strings.Split(content, "\n")
		for dy := 0; dy < r.H && dy < len(contentLines); dy++ {
			targetY := r.Y + dy
			if targetY >= height {
				break
			}
			line := contentLines[dy]
			rowRunes := []rune(rows[targetY])
			lineRunes := []rune(line)
			for dx := 0; dx < r.W && dx < len(lineRunes); dx++ {
				targetX := r.X + dx
				if targetX >= width {
					break
				}
				if targetX < len(rowRunes) {
					rowRunes[targetX] = lineRunes[dx]
				}
			}
			rows[targetY] = string(rowRunes)
		}
	}

	// Final pass: colorize border PUA markers
	for y := range rows {
		rows[y] = theme.ColorizeBorderRow(rows[y])
	}

	return strings.Join(rows, "\n")
}

func (m *Model) drawNodeBorders(node *session.LayoutNode, area session.Rect, activePane string, grid [][]rune) {
	if node.IsLeaf {
		return
	}

	switch node.Dir {
	case session.SplitVertical:
		leftW := int(float64(area.W) * node.Ratio)
		if leftW < 1 {
			leftW = 1
		}
		borderX := area.X + leftW
		if borderX < len(grid[0]) {
			for y := area.Y; y < area.Y+area.H && y < len(grid); y++ {
				grid[y][borderX] = '\u2502' // │
			}
		}
		rightW := area.W - leftW - 1
		if rightW < 1 {
			rightW = 1
		}
		m.drawNodeBorders(node.First, session.Rect{X: area.X, Y: area.Y, W: leftW, H: area.H}, activePane, grid)
		m.drawNodeBorders(node.Sec, session.Rect{X: area.X + leftW + 1, Y: area.Y, W: rightW, H: area.H}, activePane, grid)

	case session.SplitHorizontal:
		topH := int(float64(area.H) * node.Ratio)
		if topH < 1 {
			topH = 1
		}
		borderY := area.Y + topH
		if borderY < len(grid) {
			for x := area.X; x < area.X+area.W && x < len(grid[borderY]); x++ {
				grid[borderY][x] = '\u2500' // ─
			}
		}
		botH := area.H - topH - 1
		if botH < 1 {
			botH = 1
		}
		m.drawNodeBorders(node.First, session.Rect{X: area.X, Y: area.Y, W: area.W, H: topH}, activePane, grid)
		m.drawNodeBorders(node.Sec, session.Rect{X: area.X, Y: area.Y + topH + 1, W: area.W, H: botH}, activePane, grid)
	}
}

// readPty starts a goroutine to read PTY output and feed it to the terminal emulator.
func (m *Model) readPty(pane *session.Pane) tea.Cmd {
	return func() tea.Msg {
		buf := make([]byte, 32*1024)
		for {
			n, err := pane.Pty.Read(buf)
			if n > 0 {
				data := make([]byte, n)
				copy(data, buf[:n])
				_, _ = pane.Term.Write(data)
				// Feed to port tracker
				if m.tracker != nil {
					m.tracker.FeedOutput(data)
				}
				if m.program != nil {
					m.program.Send(PtyOutputMsg{PaneID: pane.ID})
				}
			}
			if err != nil {
				if err != io.EOF {
					pane.ExitErr = err
				}
				pane.HasExited = true
				return PtyExitMsg{PaneID: pane.ID, Err: err}
			}
		}
	}
}

// renderHelp shows the help overlay.
func (m *Model) renderHelp() string {
	help := `
  SAX - Terminal Multiplexer

  Prefix: Ctrl+S (then press a command key)

  Tab Management:
    c       New tab
    n / p   Next / Previous tab
    1-9     Go to tab N
    X       Close tab

  Pane Management:
    v |     Split vertical
    s -     Split horizontal
    h/j/k/l Navigate panes (vim-style)
    x       Close pane
    z       Zoom/unzoom pane

  Other:
    Ctrl+Q  Quit sax
    ?       Toggle this help

  Press any key to close help...
`
	style := lipgloss.NewStyle().
		Width(m.width).
		Height(m.height).
		Align(lipgloss.Center, lipgloss.Center)

	return style.Render(help)
}

// keyToBytes converts a bubbletea key message to raw bytes for the PTY.
func keyToBytes(msg tea.KeyMsg) []byte {
	switch msg.Type {
	case tea.KeyEnter:
		return []byte{'\r'}
	case tea.KeyTab:
		return []byte{'\t'}
	case tea.KeyBackspace:
		return []byte{127}
	case tea.KeyEscape:
		return []byte{27}
	case tea.KeyUp:
		return []byte("\x1b[A")
	case tea.KeyDown:
		return []byte("\x1b[B")
	case tea.KeyRight:
		return []byte("\x1b[C")
	case tea.KeyLeft:
		return []byte("\x1b[D")
	case tea.KeyHome:
		return []byte("\x1b[H")
	case tea.KeyEnd:
		return []byte("\x1b[F")
	case tea.KeyPgUp:
		return []byte("\x1b[5~")
	case tea.KeyPgDown:
		return []byte("\x1b[6~")
	case tea.KeyInsert:
		return []byte("\x1b[2~")
	case tea.KeyDelete:
		return []byte("\x1b[3~")
	case tea.KeySpace:
		return []byte{' '}
	case tea.KeyF1:
		return []byte("\x1bOP")
	case tea.KeyF2:
		return []byte("\x1bOQ")
	case tea.KeyF3:
		return []byte("\x1bOR")
	case tea.KeyF4:
		return []byte("\x1bOS")
	case tea.KeyF5:
		return []byte("\x1b[15~")
	case tea.KeyF6:
		return []byte("\x1b[17~")
	case tea.KeyF7:
		return []byte("\x1b[18~")
	case tea.KeyF8:
		return []byte("\x1b[19~")
	case tea.KeyF9:
		return []byte("\x1b[20~")
	case tea.KeyF10:
		return []byte("\x1b[21~")
	case tea.KeyF11:
		return []byte("\x1b[23~")
	case tea.KeyF12:
		return []byte("\x1b[24~")
	case tea.KeyCtrlA:
		return []byte{1}
	case tea.KeyCtrlB:
		return []byte{2}
	case tea.KeyCtrlC:
		return []byte{3}
	case tea.KeyCtrlD:
		return []byte{4}
	case tea.KeyCtrlE:
		return []byte{5}
	case tea.KeyCtrlF:
		return []byte{6}
	case tea.KeyCtrlG:
		return []byte{7}
	case tea.KeyCtrlH:
		return []byte{8}
	case tea.KeyCtrlK:
		return []byte{11}
	case tea.KeyCtrlL:
		return []byte{12}
	case tea.KeyCtrlN:
		return []byte{14}
	case tea.KeyCtrlO:
		return []byte{15}
	case tea.KeyCtrlP:
		return []byte{16}
	case tea.KeyCtrlR:
		return []byte{18}
	case tea.KeyCtrlT:
		return []byte{20}
	case tea.KeyCtrlU:
		return []byte{21}
	case tea.KeyCtrlV:
		return []byte{22}
	case tea.KeyCtrlW:
		return []byte{23}
	case tea.KeyCtrlX:
		return []byte{24}
	case tea.KeyCtrlY:
		return []byte{25}
	case tea.KeyCtrlZ:
		return []byte{26}
	case tea.KeyRunes:
		return []byte(string(msg.Runes))
	}

	return nil
}

// Session-level operations called from keybindings

func (m *Model) createTab() tea.Cmd {
	tab, err := m.session.AddTab()
	if err != nil {
		return nil
	}
	var cmds []tea.Cmd
	for _, pane := range tab.Panes {
		cmds = append(cmds, m.readPty(pane))
	}
	return tea.Batch(cmds...)
}

func (m *Model) nextTab() {
	m.session.NextTab()
}

func (m *Model) prevTab() {
	m.session.PrevTab()
}

func (m *Model) goToTab(idx int) {
	m.session.GoToTab(idx)
}

func (m *Model) closeTab() tea.Cmd {
	if len(m.session.Tabs) <= 1 {
		m.session.Close()
		return tea.Quit
	}
	m.session.RemoveTab(m.session.ActiveTab)
	return nil
}

func (m *Model) splitVertical() tea.Cmd {
	tab := m.session.CurrentTab()
	if tab == nil {
		return nil
	}
	cols, rows := m.session.PaneArea()
	pane, err := tab.SplitActive(session.SplitVertical, cols/2, rows)
	if err != nil {
		return nil
	}
	tab.ResizePanes(session.Rect{X: 0, Y: 0, W: cols, H: rows})
	return m.readPty(pane)
}

func (m *Model) splitHorizontal() tea.Cmd {
	tab := m.session.CurrentTab()
	if tab == nil {
		return nil
	}
	cols, rows := m.session.PaneArea()
	pane, err := tab.SplitActive(session.SplitHorizontal, cols, rows/2)
	if err != nil {
		return nil
	}
	tab.ResizePanes(session.Rect{X: 0, Y: 0, W: cols, H: rows})
	return m.readPty(pane)
}

func (m *Model) navigatePane(dir Direction) {
	tab := m.session.CurrentTab()
	if tab == nil {
		return
	}
	cols, rows := m.session.PaneArea()
	rects := tab.Layout.Arrange(session.Rect{X: 0, Y: 0, W: cols, H: rows})

	var splitDir session.SplitDir
	var second bool
	switch dir {
	case DirLeft:
		splitDir = session.SplitVertical
		second = false
	case DirRight:
		splitDir = session.SplitVertical
		second = true
	case DirUp:
		splitDir = session.SplitHorizontal
		second = false
	case DirDown:
		splitDir = session.SplitHorizontal
		second = true
	}

	newPane := tab.Layout.FindNeighbor(tab.ActivePane, splitDir, second, rects)
	tab.ActivePane = newPane
}

func (m *Model) closePane() tea.Cmd {
	tab := m.session.CurrentTab()
	if tab == nil {
		return nil
	}

	if len(m.session.Tabs) == 1 && tab.Layout.PaneCount() <= 1 {
		m.session.Close()
		return tea.Quit
	}

	paneID := tab.ActivePane
	alive := tab.RemovePane(paneID)
	if !alive {
		m.session.RemoveTab(m.session.ActiveTab)
		if len(m.session.Tabs) == 0 {
			return tea.Quit
		}
	} else {
		cols, rows := m.session.PaneArea()
		tab.ResizePanes(session.Rect{X: 0, Y: 0, W: cols, H: rows})
	}
	return nil
}

func (m *Model) toggleZoom() {
	tab := m.session.CurrentTab()
	if tab == nil {
		return
	}

	if tab.Zoomed {
		tab.Zoomed = false
		tab.ZoomedPane = ""
		cols, rows := m.session.PaneArea()
		tab.ResizePanes(session.Rect{X: 0, Y: 0, W: cols, H: rows})
	} else {
		tab.Zoomed = true
		tab.ZoomedPane = tab.ActivePane
		cols, rows := m.session.PaneArea()
		if p, ok := tab.Panes[tab.ActivePane]; ok {
			p.Resize(cols, rows)
		}
	}
}
