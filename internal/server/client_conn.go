package server

import (
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/asc/sax/internal/lock"
	"github.com/asc/sax/internal/logger"
	"github.com/asc/sax/internal/scrollback"
	"github.com/asc/sax/internal/session"
	"github.com/asc/sax/internal/statusbar"
)

// ClientConn represents a connected client.
type ClientConn struct {
	ID     string
	conn   net.Conn
	writer *ConnWriter
	ms     *ManagedSession
	srv    *Server

	width  int
	height int
	sizeMu sync.RWMutex

	dirty    atomic.Bool
	done     chan struct{}
	closeOnce sync.Once

	// Client-specific state
	copyMode   bool
	showHelp   bool
	prefixMode bool
	lockScreen bool
	lockPwHash []byte
	lockInput  string
	windowList bool

	// Copy mode navigator (set when in copy mode)
	copyNav *scrollback.Navigator
}

var clientCounter atomic.Uint64

func newClientConn(conn net.Conn, ms *ManagedSession, srv *Server) *ClientConn {
	id := fmt.Sprintf("client-%d", clientCounter.Add(1))
	return &ClientConn{
		ID:     id,
		conn:   conn,
		writer: NewConnWriter(conn),
		ms:     ms,
		srv:    srv,
		done:   make(chan struct{}),
	}
}

// Size returns the client's terminal dimensions.
func (cc *ClientConn) Size() (int, int) {
	cc.sizeMu.RLock()
	defer cc.sizeMu.RUnlock()
	return cc.width, cc.height
}

// SetSize updates the client's terminal dimensions.
func (cc *ClientConn) SetSize(w, h int) {
	cc.sizeMu.Lock()
	cc.width = w
	cc.height = h
	cc.sizeMu.Unlock()
}

// MarkDirty signals that the client needs a frame update.
func (cc *ClientConn) MarkDirty() {
	cc.dirty.Store(true)
}

// Close disconnects the client.
func (cc *ClientConn) Close() {
	cc.closeOnce.Do(func() {
		close(cc.done)
		cc.conn.Close()
	})
}

// Run starts the client's read and render loops.
func (cc *ClientConn) Run() {
	go cc.readLoop()
	go cc.renderLoop()
}

// readLoop reads messages from the client and dispatches them.
func (cc *ClientConn) readLoop() {
	defer func() {
		cc.ms.RemoveClient(cc.ID)
		cc.Close()
	}()

	for {
		select {
		case <-cc.done:
			return
		default:
		}

		env, err := ReadMsg(cc.conn)
		if err != nil {
			if err != io.EOF {
				select {
				case <-cc.done:
				default:
					log.Printf("client %s read error: %v", cc.ID, err)
				}
			}
			return
		}

		cc.dispatch(env)
	}
}

// renderLoop sends frames to the client at up to 60fps when dirty.
func (cc *ClientConn) renderLoop() {
	ticker := time.NewTicker(16 * time.Millisecond) // ~60fps
	defer ticker.Stop()

	for {
		select {
		case <-cc.done:
			return
		case <-ticker.C:
			if !cc.dirty.CompareAndSwap(true, false) {
				continue
			}
			cc.sendFrame()
		}
	}
}

// sendFrame renders and sends the current frame.
func (cc *ClientConn) sendFrame() {
	w, h := cc.Size()
	if w == 0 || h == 0 {
		return
	}

	var mode statusbar.Mode
	if cc.prefixMode {
		mode = statusbar.ModePrefix
	}

	ctx := &RenderContext{
		Session:    cc.ms.Session,
		Mode:       mode,
		Ports:      cc.ms.Ports,
		ShowHelp:   cc.showHelp,
		CopyMode:   cc.copyMode,
		LockScreen: cc.lockScreen,
		WindowList: cc.windowList,
		Width:      w,
		Height:     h,
	}

	if cc.lockScreen {
		ctx.LockPrompt = "Enter password: " + maskString(cc.lockInput)
	}

	if cc.copyMode && cc.copyNav != nil {
		ctx.CopyOverlay = cc.copyNav.Render(w, h)
	}

	if cc.windowList {
		ctx.WindowListOverlay = renderWindowList(cc.ms.Session, w, h)
	}

	frame := RenderFrame(ctx)

	// Set write deadline to prevent slow client from blocking
	cc.conn.SetWriteDeadline(time.Now().Add(100 * time.Millisecond))
	_ = cc.writer.WriteMsg(MsgFrame, &FrameMsg{
		Content: frame,
		W:       w,
		H:       h,
	})
	cc.conn.SetWriteDeadline(time.Time{})
}

// dispatch routes a message to the appropriate handler.
func (cc *ClientConn) dispatch(env *Envelope) {
	switch env.Type {
	case MsgAttach:
		var msg AttachMsg
		if err := DecodeData(env, &msg); err != nil {
			return
		}
		cc.handleAttach(msg)

	case MsgKeyInput:
		var msg KeyInputMsg
		if err := DecodeData(env, &msg); err != nil {
			return
		}
		cc.handleKeyInput(msg)

	case MsgResize:
		var msg ResizeMsg
		if err := DecodeData(env, &msg); err != nil {
			return
		}
		cc.handleResize(msg)

	case MsgCommand:
		var msg CommandMsg
		if err := DecodeData(env, &msg); err != nil {
			return
		}
		cc.handleCommand(msg)
	}
}

// handleAttach processes the initial attach handshake.
func (cc *ClientConn) handleAttach(msg AttachMsg) {
	cc.SetSize(msg.W, msg.H)
	cc.ms.recalcSize()

	// Send snapshot
	tabNames := cc.ms.Session.TabNames()
	w, h := cc.Size()

	ctx := &RenderContext{
		Session: cc.ms.Session,
		Width:   w,
		Height:  h,
	}
	frame := RenderFrame(ctx)

	_ = cc.writer.WriteMsg(MsgSnapshot, &SnapshotMsg{
		Name:      cc.ms.Name,
		Tabs:      tabNames,
		ActiveTab: cc.ms.Session.ActiveTab,
		Frame:     frame,
		W:         w,
		H:         h,
	})
}

// handleKeyInput forwards raw key bytes to the active pane's PTY.
func (cc *ClientConn) handleKeyInput(msg KeyInputMsg) {
	if cc.lockScreen {
		cc.handleLockInput(msg.Data)
		return
	}

	if cc.windowList {
		cc.handleWindowListInput(msg.Data)
		return
	}

	if cc.copyMode && cc.copyNav != nil {
		cc.handleCopyModeInput(msg.Data)
		return
	}

	pane := cc.ms.Session.ActivePane()
	if pane != nil && !pane.HasExited {
		_, _ = pane.Pty.Write(msg.Data)
	}
}

// handleCopyModeInput routes key input to the scrollback navigator.
func (cc *ClientConn) handleCopyModeInput(data []byte) {
	// Map raw bytes to action strings
	for _, b := range data {
		var action string
		switch b {
		case 'j':
			action = "j"
		case 'k':
			action = "k"
		case 'h':
			action = "h"
		case 'l':
			action = "l"
		case 'g':
			action = "g"
		case 'G':
			action = "G"
		case ' ':
			action = "space"
		case 'y':
			action = "y"
		case 'q':
			action = "q"
		case '/':
			action = "/"
		case 'n':
			action = "n"
		case 'N':
			action = "N"
		case '0':
			action = "0"
		case '$':
			action = "$"
		case 6: // Ctrl+F
			action = "ctrl+f"
		case 2: // Ctrl+B
			action = "ctrl+b"
		case 4: // Ctrl+D
			action = "ctrl+d"
		case 21: // Ctrl+U
			action = "ctrl+u"
		case 27: // Escape
			action = "escape"
		case '\r', '\n':
			action = "enter"
		case 127, 8: // Backspace
			action = "backspace"
		default:
			if b >= 32 && b < 127 {
				action = string(b)
			}
			continue
		}

		exit, yanked := cc.copyNav.HandleKey(action)
		if yanked != "" {
			cc.ms.SetCopyBuffer(yanked)
			_ = cc.writer.WriteMsg(MsgCopyBuffer, &CopyBufferMsg{Content: yanked})
		}
		if exit {
			cc.copyMode = false
			cc.copyNav = nil
		}
		cc.MarkDirty()
	}
}

// handleResize processes a terminal resize from the client.
func (cc *ClientConn) handleResize(msg ResizeMsg) {
	cc.SetSize(msg.W, msg.H)
	cc.ms.recalcSize()
	cc.MarkDirty()
}

// handleCommand processes commands from the client.
func (cc *ClientConn) handleCommand(msg CommandMsg) {
	switch msg.Cmd {
	case CmdCreateTab:
		hadTabBar := cc.ms.Session.ShowTabBar()
		tab, err := cc.ms.Session.AddTab()
		if err != nil {
			return
		}
		// Resize panes if tab bar visibility changed (1→2 tabs)
		if !hadTabBar && cc.ms.Session.ShowTabBar() {
			cc.ms.recalcSize()
		}
		for _, pane := range tab.Panes {
			cc.ms.startPaneReader(pane)
		}
		cc.ms.MarkDirty()

	case CmdCloseTab:
		sess := cc.ms.Session
		if len(sess.Tabs) <= 1 {
			// Last tab — close session
			cc.ms.broadcastEvent(EventSessionClosed, "")
			cc.srv.sessions.Remove(cc.ms.Name)
			return
		}
		hadTabBar := sess.ShowTabBar()
		sess.RemoveTab(sess.ActiveTab)
		// Resize panes if tab bar visibility changed (2→1 tabs)
		if hadTabBar && !sess.ShowTabBar() {
			cc.ms.recalcSize()
		}
		cc.ms.MarkDirty()

	case CmdNextTab:
		cc.ms.Session.NextTab()
		cc.ms.MarkDirty()

	case CmdPrevTab:
		cc.ms.Session.PrevTab()
		cc.ms.MarkDirty()

	case CmdGoToTab:
		var idx int
		if _, err := fmt.Sscanf(msg.Args, "%d", &idx); err == nil {
			cc.ms.Session.GoToTab(idx)
			cc.ms.MarkDirty()
		}

	case CmdSplitV:
		tab := cc.ms.Session.CurrentTab()
		if tab == nil {
			return
		}
		cols, rows := cc.ms.Session.PaneArea()
		pane, err := tab.SplitActive(session.SplitVertical, cols/2, rows)
		if err != nil {
			return
		}
		tab.ResizePanes(session.Rect{X: 0, Y: 0, W: cols, H: rows})
		cc.ms.startPaneReader(pane)
		cc.ms.MarkDirty()

	case CmdSplitH:
		tab := cc.ms.Session.CurrentTab()
		if tab == nil {
			return
		}
		cols, rows := cc.ms.Session.PaneArea()
		pane, err := tab.SplitActive(session.SplitHorizontal, cols, rows/2)
		if err != nil {
			return
		}
		tab.ResizePanes(session.Rect{X: 0, Y: 0, W: cols, H: rows})
		cc.ms.startPaneReader(pane)
		cc.ms.MarkDirty()

	case CmdClosePane:
		tab := cc.ms.Session.CurrentTab()
		if tab == nil {
			return
		}
		sess := cc.ms.Session
		if len(sess.Tabs) == 1 && tab.Layout.PaneCount() <= 1 {
			cc.ms.broadcastEvent(EventSessionClosed, "")
			cc.srv.sessions.Remove(cc.ms.Name)
			return
		}
		hadTabBar := sess.ShowTabBar()
		paneID := tab.ActivePane
		alive := tab.RemovePane(paneID)
		if !alive {
			sess.RemoveTab(sess.ActiveTab)
			if len(sess.Tabs) == 0 {
				cc.ms.broadcastEvent(EventSessionClosed, "")
				cc.srv.sessions.Remove(cc.ms.Name)
				return
			}
		} else {
			cols, rows := sess.PaneArea()
			tab.ResizePanes(session.Rect{X: 0, Y: 0, W: cols, H: rows})
		}
		// Resize panes if tab bar visibility changed (2→1 tabs)
		if hadTabBar && !sess.ShowTabBar() {
			cc.ms.recalcSize()
		}
		cc.ms.MarkDirty()

	case CmdNavPane:
		tab := cc.ms.Session.CurrentTab()
		if tab == nil {
			return
		}
		cols, rows := cc.ms.Session.PaneArea()
		rects := tab.Layout.Arrange(session.Rect{X: 0, Y: 0, W: cols, H: rows})

		var splitDir session.SplitDir
		var second bool
		switch msg.Args {
		case "left":
			splitDir = session.SplitVertical
			second = false
		case "right":
			splitDir = session.SplitVertical
			second = true
		case "up":
			splitDir = session.SplitHorizontal
			second = false
		case "down":
			splitDir = session.SplitHorizontal
			second = true
		}
		newPane := tab.Layout.FindNeighbor(tab.ActivePane, splitDir, second, rects)
		tab.ActivePane = newPane
		cc.ms.MarkDirty()

	case CmdZoom:
		tab := cc.ms.Session.CurrentTab()
		if tab == nil {
			return
		}
		if tab.Zoomed {
			tab.Zoomed = false
			tab.ZoomedPane = ""
			cols, rows := cc.ms.Session.PaneArea()
			tab.ResizePanes(session.Rect{X: 0, Y: 0, W: cols, H: rows})
		} else {
			tab.Zoomed = true
			tab.ZoomedPane = tab.ActivePane
			cols, rows := cc.ms.Session.PaneArea()
			if p, ok := tab.Panes[tab.ActivePane]; ok {
				p.Resize(cols, rows)
			}
		}
		cc.ms.MarkDirty()

	case CmdDetach:
		cc.Close()

	case CmdHelp:
		cc.showHelp = !cc.showHelp
		cc.MarkDirty()

	case CmdPrefixMode:
		cc.prefixMode = msg.Args == "true"
		cc.MarkDirty()

	case CmdEnterCopyMode:
		pane := cc.ms.Session.ActivePane()
		if pane == nil {
			return
		}
		w, h := cc.Size()
		cc.copyNav = scrollback.NewNavigator(pane.Term.Scrollback, w, h-2)
		cc.copyMode = true
		cc.MarkDirty()

	case CmdPaste:
		content := cc.ms.GetCopyBuffer()
		if content != "" {
			pane := cc.ms.Session.ActivePane()
			if pane != nil && !pane.HasExited {
				_, _ = pane.Pty.Write([]byte(content))
			}
		}

	case CmdToggleLog:
		pane := cc.ms.Session.ActivePane()
		if pane == nil {
			return
		}
		if cc.ms.Logger != nil {
			cc.ms.Logger.Toggle(pane.ID)
		}
		cc.ms.MarkDirty()

	case CmdHardcopy:
		pane := cc.ms.Session.ActivePane()
		if pane != nil {
			content := pane.Render()
			if path, err := logger.Hardcopy(content); err == nil {
				_ = cc.writer.WriteMsg(MsgSessionEvent, &SessionEventMsg{
					Event: "hardcopy",
					Data:  path,
				})
			}
		}
		cc.ms.MarkDirty()

	case CmdLock:
		cc.lockScreen = true
		cc.lockInput = ""
		cc.MarkDirty()

	case CmdUnlock:
		cc.lockScreen = false
		cc.lockInput = ""
		cc.lockPwHash = nil
		cc.MarkDirty()

	case CmdMonitorAct:
		pane := cc.ms.Session.ActivePane()
		if pane == nil {
			return
		}
		if cc.ms.Monitor != nil {
			cc.ms.Monitor.ToggleActivity(pane.ID)
		}
		cc.ms.MarkDirty()

	case CmdMonitorSil:
		pane := cc.ms.Session.ActivePane()
		if pane == nil {
			return
		}
		if cc.ms.Monitor != nil {
			cc.ms.Monitor.ToggleSilence(pane.ID)
		}
		cc.ms.MarkDirty()

	case CmdWindowList:
		cc.windowList = !cc.windowList
		cc.MarkDirty()
	}
}

// handleLockInput processes key input while the screen is locked.
func (cc *ClientConn) handleLockInput(data []byte) {
	for _, b := range data {
		switch b {
		case '\r', '\n':
			if cc.lockPwHash == nil {
				// First time — set password with bcrypt
				if len(cc.lockInput) > 0 {
					hash, err := lock.HashPassword(cc.lockInput)
					if err == nil {
						cc.lockPwHash = hash
					}
					cc.lockInput = ""
				}
			} else {
				// Verify password with bcrypt
				if lock.CheckPassword(cc.lockPwHash, cc.lockInput) {
					cc.lockScreen = false
					cc.lockInput = ""
					cc.lockPwHash = nil
				} else {
					cc.lockInput = ""
				}
			}
			cc.MarkDirty()
		case 127, 8: // backspace
			if len(cc.lockInput) > 0 {
				cc.lockInput = cc.lockInput[:len(cc.lockInput)-1]
				cc.MarkDirty()
			}
		case 27: // escape — cancel lock if no password set yet
			if cc.lockPwHash == nil {
				cc.lockScreen = false
				cc.lockInput = ""
				cc.MarkDirty()
			}
		default:
			if b >= 32 && b < 127 {
				cc.lockInput += string(b)
				cc.MarkDirty()
			}
		}
	}
}

func maskString(s string) string {
	result := make([]byte, len(s))
	for i := range result {
		result[i] = '*'
	}
	return string(result)
}

// renderWindowList generates the window list overlay.
func renderWindowList(sess *session.Session, width, height int) string {
	var lines []string
	lines = append(lines, "+--- Window List ---+")

	for i, tab := range sess.Tabs {
		prefix := "  "
		if i == sess.ActiveTab {
			prefix = "> "
		}
		name := fmt.Sprintf("%s%d: %s (%d panes)", prefix, i+1, tab.Name, tab.Layout.PaneCount())
		if len(name) > width-4 {
			name = name[:width-4]
		}
		lines = append(lines, "| "+name+" |")
	}

	lines = append(lines, "+-------------------+")
	lines = append(lines, "| [n/p] nav [Enter] |")
	lines = append(lines, "+-------------------+")

	return strings.Join(lines, "\n")
}

// handleWindowListInput processes key input for the window list overlay.
func (cc *ClientConn) handleWindowListInput(data []byte) {
	for _, b := range data {
		switch b {
		case 'j', 'n': // next
			cc.ms.Session.NextTab()
		case 'k', 'p': // prev
			cc.ms.Session.PrevTab()
		case '\r', '\n': // select
			cc.windowList = false
		case 'q', 27: // quit
			cc.windowList = false
		case '1', '2', '3', '4', '5', '6', '7', '8', '9':
			idx := int(b - '1')
			cc.ms.Session.GoToTab(idx)
			cc.windowList = false
		}
		cc.ms.MarkDirty()
	}
}
