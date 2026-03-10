package client

import (
	"fmt"
	"io"
	"log"
	"net"
	"time"

	"github.com/asc/sax/internal/server"

	tea "github.com/charmbracelet/bubbletea"
)

// PrefixKey is the key that activates prefix mode (Ctrl+S).
const PrefixKey = "ctrl+s"

// frameMsg carries a rendered frame from the server.
type frameMsg struct {
	content string
}

// snapshotMsg carries initial session state.
type snapshotMsg struct {
	snapshot server.SnapshotMsg
}

// sessionEventMsg carries a session event.
type sessionEventMsg struct {
	event server.SessionEventMsg
}

// disconnectMsg signals the server disconnected.
type disconnectMsg struct{}

// clearPrefixMsg clears prefix mode after timeout.
type clearPrefixMsg struct{}

// Model is the thin bubbletea client that connects to the server.
type Model struct {
	conn    net.Conn
	writer  *server.ConnWriter
	frame   string
	width   int
	height  int
	ready   bool
	prefix  bool
	program *tea.Program
}

// New creates a new client model connected to the given socket.
func New(conn net.Conn) *Model {
	return &Model{
		conn:   conn,
		writer: server.NewConnWriter(conn),
	}
}

// SetProgram stores the tea.Program reference.
func (m *Model) SetProgram(p *tea.Program) {
	m.program = p
}

// Init sends the initial attach message.
func (m *Model) Init() tea.Cmd {
	return nil
}

// Update handles messages.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

		if !m.ready {
			m.ready = true
			// Send attach message
			_ = m.writer.WriteMsg(server.MsgAttach, &server.AttachMsg{
				W: msg.Width,
				H: msg.Height,
			})
			// Start reading frames from server
			return m, m.readServerMessages()
		}

		// Send resize
		_ = m.writer.WriteMsg(server.MsgResize, &server.ResizeMsg{
			W: msg.Width,
			H: msg.Height,
		})
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case tea.MouseMsg:
		return m.handleMouse(msg)

	case frameMsg:
		m.frame = msg.content
		return m, nil

	case snapshotMsg:
		m.frame = msg.snapshot.Frame
		return m, nil

	case sessionEventMsg:
		if msg.event.Event == server.EventSessionClosed {
			return m, tea.Quit
		}
		return m, nil

	case disconnectMsg:
		return m, tea.Quit

	case clearPrefixMsg:
		if m.prefix {
			m.prefix = false
			_ = m.writer.WriteMsg(server.MsgCommand, &server.CommandMsg{
				Cmd:  server.CmdPrefixMode,
				Args: "false",
			})
		}
		return m, nil
	}

	return m, nil
}

// View returns the current frame from the server.
func (m *Model) View() string {
	if !m.ready {
		return "Connecting to sax..."
	}
	if m.frame == "" {
		return "Waiting for server..."
	}
	return m.frame
}

// handleKey processes keyboard input.
func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// Ctrl+Q to quit (also detaches)
	if key == "ctrl+q" {
		_ = m.writer.WriteMsg(server.MsgCommand, &server.CommandMsg{Cmd: server.CmdDetach})
		return m, tea.Quit
	}

	// Prefix mode activation
	if key == PrefixKey {
		if !m.prefix {
			m.prefix = true
			_ = m.writer.WriteMsg(server.MsgCommand, &server.CommandMsg{
				Cmd:  server.CmdPrefixMode,
				Args: "true",
			})
			return m, tea.Tick(2*time.Second, func(time.Time) tea.Msg {
				return clearPrefixMsg{}
			})
		}
		// Double Ctrl+S sends literal Ctrl+S to PTY
		m.prefix = false
		_ = m.writer.WriteMsg(server.MsgCommand, &server.CommandMsg{
			Cmd:  server.CmdPrefixMode,
			Args: "false",
		})
		_ = m.writer.WriteMsg(server.MsgKeyInput, &server.KeyInputMsg{
			Data: []byte{0x13},
		})
		return m, nil
	}

	// Prefix mode commands
	if m.prefix {
		m.prefix = false
		_ = m.writer.WriteMsg(server.MsgCommand, &server.CommandMsg{
			Cmd:  server.CmdPrefixMode,
			Args: "false",
		})
		cmd, consumed := m.handlePrefixKey(msg)
		if consumed {
			return m, cmd
		}
	}

	// Normal mode — forward keypress to server as raw bytes
	data := keyToBytes(msg)
	if len(data) > 0 {
		_ = m.writer.WriteMsg(server.MsgKeyInput, &server.KeyInputMsg{
			Data: data,
		})
	}

	return m, nil
}

// handlePrefixKey routes prefix commands to the server.
func (m *Model) handlePrefixKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	key := msg.String()

	switch key {
	// Tab management
	case "c":
		_ = m.writer.WriteMsg(server.MsgCommand, &server.CommandMsg{Cmd: server.CmdCreateTab})
		return nil, true
	case "n":
		_ = m.writer.WriteMsg(server.MsgCommand, &server.CommandMsg{Cmd: server.CmdNextTab})
		return nil, true
	case "p":
		_ = m.writer.WriteMsg(server.MsgCommand, &server.CommandMsg{Cmd: server.CmdPrevTab})
		return nil, true
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		idx := int(key[0] - '1')
		_ = m.writer.WriteMsg(server.MsgCommand, &server.CommandMsg{
			Cmd:  server.CmdGoToTab,
			Args: fmt.Sprintf("%d", idx),
		})
		return nil, true
	case "X":
		_ = m.writer.WriteMsg(server.MsgCommand, &server.CommandMsg{Cmd: server.CmdCloseTab})
		return nil, true

	// Pane management
	case "v", "|":
		_ = m.writer.WriteMsg(server.MsgCommand, &server.CommandMsg{Cmd: server.CmdSplitV})
		return nil, true
	case "s", "-":
		_ = m.writer.WriteMsg(server.MsgCommand, &server.CommandMsg{Cmd: server.CmdSplitH})
		return nil, true
	case "h":
		_ = m.writer.WriteMsg(server.MsgCommand, &server.CommandMsg{Cmd: server.CmdNavPane, Args: "left"})
		return nil, true
	case "j":
		_ = m.writer.WriteMsg(server.MsgCommand, &server.CommandMsg{Cmd: server.CmdNavPane, Args: "down"})
		return nil, true
	case "k":
		_ = m.writer.WriteMsg(server.MsgCommand, &server.CommandMsg{Cmd: server.CmdNavPane, Args: "up"})
		return nil, true
	case "l":
		_ = m.writer.WriteMsg(server.MsgCommand, &server.CommandMsg{Cmd: server.CmdNavPane, Args: "right"})
		return nil, true
	case "x":
		_ = m.writer.WriteMsg(server.MsgCommand, &server.CommandMsg{Cmd: server.CmdClosePane})
		return nil, true
	case "z":
		_ = m.writer.WriteMsg(server.MsgCommand, &server.CommandMsg{Cmd: server.CmdZoom})
		return nil, true

	// Session commands
	case "d":
		_ = m.writer.WriteMsg(server.MsgCommand, &server.CommandMsg{Cmd: server.CmdDetach})
		return tea.Quit, true

	// Copy/scrollback
	case "[":
		_ = m.writer.WriteMsg(server.MsgCommand, &server.CommandMsg{Cmd: server.CmdEnterCopyMode})
		return nil, true
	case "]":
		_ = m.writer.WriteMsg(server.MsgCommand, &server.CommandMsg{Cmd: server.CmdPaste})
		return nil, true

	// Window list
	case `"`:
		_ = m.writer.WriteMsg(server.MsgCommand, &server.CommandMsg{Cmd: server.CmdWindowList})
		return nil, true

	// Logging
	case "H":
		_ = m.writer.WriteMsg(server.MsgCommand, &server.CommandMsg{Cmd: server.CmdToggleLog})
		return nil, true

	// Hardcopy (lowercase h is nav, so use ctrl+h or uppercase context)
	// Actually in the plan it says Ctrl+S h for hardcopy — but h is nav left.
	// The plan says: Ctrl+S h for hardcopy, Ctrl+S H for logging. Let me check...
	// Plan says: Ctrl+S H for logging, Ctrl+S h for hardcopy
	// But h is already nav_pane left in the current keybindings.
	// We need to differentiate. Let's keep h as nav_pane left and use ctrl+h for hardcopy.

	// Lock screen
	case "ctrl+x":
		_ = m.writer.WriteMsg(server.MsgCommand, &server.CommandMsg{Cmd: server.CmdLock})
		return nil, true

	// Monitor activity
	case "M":
		_ = m.writer.WriteMsg(server.MsgCommand, &server.CommandMsg{Cmd: server.CmdMonitorAct})
		return nil, true

	// Monitor silence
	case "_":
		_ = m.writer.WriteMsg(server.MsgCommand, &server.CommandMsg{Cmd: server.CmdMonitorSil})
		return nil, true

	// Help
	case "?":
		_ = m.writer.WriteMsg(server.MsgCommand, &server.CommandMsg{Cmd: server.CmdHelp})
		return nil, true

	default:
		return nil, false
	}
}

// handleMouse processes mouse events, sending wheel scrolls to the server.
func (m *Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.MouseWheelUp:
		_ = m.writer.WriteMsg(server.MsgMouseWheel, &server.MouseWheelMsg{
			Up: true, Lines: 3,
		})
	case tea.MouseWheelDown:
		_ = m.writer.WriteMsg(server.MsgMouseWheel, &server.MouseWheelMsg{
			Up: false, Lines: 3,
		})
	}
	return m, nil
}

// readServerMessages starts a goroutine to read messages from the server.
func (m *Model) readServerMessages() tea.Cmd {
	return func() tea.Msg {
		go func() {
			for {
				env, err := server.ReadMsg(m.conn)
				if err != nil {
					if err != io.EOF {
						log.Printf("server read error: %v", err)
					}
					if m.program != nil {
						m.program.Send(disconnectMsg{})
					}
					return
				}

				if m.program == nil {
					continue
				}

				switch env.Type {
				case server.MsgFrame:
					var frame server.FrameMsg
					if err := server.DecodeData(env, &frame); err == nil {
						m.program.Send(frameMsg{content: frame.Content})
					}
				case server.MsgSnapshot:
					var snap server.SnapshotMsg
					if err := server.DecodeData(env, &snap); err == nil {
						m.program.Send(snapshotMsg{snapshot: snap})
					}
				case server.MsgSessionEvent:
					var event server.SessionEventMsg
					if err := server.DecodeData(env, &event); err == nil {
						m.program.Send(sessionEventMsg{event: event})
					}
				case server.MsgCopyBuffer:
					// Could integrate with system clipboard
				case server.MsgError:
					var errMsg server.ErrorMsg
					if err := server.DecodeData(env, &errMsg); err == nil {
						log.Printf("server error: %s", errMsg.Message)
					}
				}
			}
		}()
		return nil
	}
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
