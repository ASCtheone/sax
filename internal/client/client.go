package client

import (
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/asc/sax/internal/saxrc"
	"github.com/asc/sax/internal/server"

	tea "github.com/charmbracelet/bubbletea"
)

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
	conn         net.Conn
	writer       *server.ConnWriter
	frame        string
	width        int
	height       int
	ready        bool
	prefixActive bool
	program      *tea.Program

	// prefixKey and keymap are resolved from ~/.saxrc at construction. keymap
	// maps a key string to a sax command string used in prefix mode.
	prefixKey string
	keymap    map[string]string
}

// New creates a new client model connected to the given socket. It loads
// ~/.saxrc to resolve the prefix key and prefix-mode keybindings.
func New(conn net.Conn) *Model {
	prefixKey, keymap := buildKeymap(saxrc.Load())
	return &Model{
		conn:      conn,
		writer:    server.NewConnWriter(conn),
		prefixKey: prefixKey,
		keymap:    keymap,
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
		if m.prefixActive {
			m.prefixActive = false
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

	// Prefix key activation
	if key == m.prefixKey {
		if !m.prefixActive {
			m.prefixActive = true
			_ = m.writer.WriteMsg(server.MsgCommand, &server.CommandMsg{
				Cmd:  server.CmdPrefixMode,
				Args: "true",
			})
			return m, tea.Tick(2*time.Second, func(time.Time) tea.Msg {
				return clearPrefixMsg{}
			})
		}
		// Pressing the prefix twice sends it through to the PTY literally.
		m.prefixActive = false
		_ = m.writer.WriteMsg(server.MsgCommand, &server.CommandMsg{
			Cmd:  server.CmdPrefixMode,
			Args: "false",
		})
		_ = m.writer.WriteMsg(server.MsgKeyInput, &server.KeyInputMsg{
			Data: prefixLiteralBytes(m.prefixKey),
		})
		return m, nil
	}

	// Prefix mode commands
	if m.prefixActive {
		m.prefixActive = false
		_ = m.writer.WriteMsg(server.MsgCommand, &server.CommandMsg{
			Cmd:  server.CmdPrefixMode,
			Args: "false",
		})
		if cmdStr, ok := m.keymap[key]; ok {
			cmd, consumed := m.dispatchCommand(cmdStr)
			if consumed {
				return m, cmd
			}
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

// dispatchCommand translates a sax command string (from the keymap) into a
// protocol command sent to the server. It returns an optional tea.Cmd and
// whether the command was recognized.
func (m *Model) dispatchCommand(cmdStr string) (tea.Cmd, bool) {
	fields := strings.Fields(cmdStr)
	if len(fields) == 0 {
		return nil, false
	}
	name, args := fields[0], fields[1:]

	send := func(cmd, cmdArgs string) {
		_ = m.writer.WriteMsg(server.MsgCommand, &server.CommandMsg{Cmd: cmd, Args: cmdArgs})
	}

	switch name {
	case "new-tab", "new-window":
		send(server.CmdCreateTab, "")
	case "next-tab":
		send(server.CmdNextTab, "")
	case "prev-tab":
		send(server.CmdPrevTab, "")
	case "select-tab", "go-to-tab":
		idx := 0
		if len(args) > 0 {
			if n, err := strconv.Atoi(args[0]); err == nil {
				idx = n - 1 // .saxrc / UI tabs are 1-based
			}
		}
		send(server.CmdGoToTab, fmt.Sprintf("%d", idx))
	case "close-tab":
		send(server.CmdCloseTab, "")
	case "split-v", "split-vertical":
		send(server.CmdSplitV, "")
	case "split-h", "split-horizontal":
		send(server.CmdSplitH, "")
	case "pane-left":
		send(server.CmdNavPane, "left")
	case "pane-right":
		send(server.CmdNavPane, "right")
	case "pane-up":
		send(server.CmdNavPane, "up")
	case "pane-down":
		send(server.CmdNavPane, "down")
	case "close-pane":
		send(server.CmdClosePane, "")
	case "zoom":
		send(server.CmdZoom, "")
	case "detach":
		send(server.CmdDetach, "")
		return tea.Quit, true
	case "copy-mode":
		send(server.CmdEnterCopyMode, "")
	case "paste":
		send(server.CmdPaste, "")
	case "window-list":
		send(server.CmdWindowList, "")
	case "toggle-log":
		send(server.CmdToggleLog, "")
	case "lock":
		send(server.CmdLock, "")
	case "monitor-activity":
		send(server.CmdMonitorAct, "")
	case "monitor-silence":
		send(server.CmdMonitorSil, "")
	case "help":
		send(server.CmdHelp, "")
	default:
		return nil, false
	}
	return nil, true
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
					// The server also prepends an OSC 52 sequence to the
					// next frame, which the user's outer terminal acts on
					// to set the system clipboard. Nothing to do here.
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
