package server

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

// Message types for client → server
const (
	MsgKeyInput      = "key_input"
	MsgResize        = "resize"
	MsgCommand       = "command"
	MsgCopyModeInput = "copy_mode_input"
	MsgAttach        = "attach"
	MsgQuery         = "query"
	MsgMouseWheel    = "mouse_wheel"
)

// Message types for server → client
const (
	MsgFrame        = "frame"
	MsgSnapshot     = "snapshot"
	MsgSessionEvent = "session_event"
	MsgCopyBuffer   = "copy_buffer"
	MsgError        = "error"
	MsgQueryReply   = "query_reply"
)

// Command types
const (
	CmdCreateTab     = "create_tab"
	CmdCloseTab      = "close_tab"
	CmdNextTab       = "next_tab"
	CmdPrevTab       = "prev_tab"
	CmdGoToTab       = "go_to_tab"
	CmdSplitV        = "split_v"
	CmdSplitH        = "split_h"
	CmdClosePane     = "close_pane"
	CmdNavPane       = "nav_pane"
	CmdZoom          = "zoom"
	CmdDetach        = "detach"
	CmdEnterCopyMode = "enter_copy_mode"
	CmdPaste         = "paste"
	CmdToggleLog     = "toggle_log"
	CmdHardcopy      = "hardcopy"
	CmdLock          = "lock"
	CmdUnlock        = "unlock"
	CmdMonitorAct    = "monitor_activity"
	CmdMonitorSil    = "monitor_silence"
	CmdWindowList    = "window_list"
	CmdHelp          = "help"
	CmdPrefixMode    = "prefix_mode"
)

// Session event types
const (
	EventTabChanged    = "tab_changed"
	EventPaneChanged   = "pane_changed"
	EventPortUpdate    = "port_update"
	EventActivity      = "activity"
	EventSilence       = "silence"
	EventSessionClosed = "session_closed"
)

// Envelope wraps all IPC messages with a type discriminator.
type Envelope struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

// --- Client → Server messages ---

// KeyInputMsg carries raw key bytes from the client.
type KeyInputMsg struct {
	Data []byte `json:"data"`
}

// ResizeMsg carries terminal dimensions from the client.
type ResizeMsg struct {
	W int `json:"w"`
	H int `json:"h"`
}

// CommandMsg carries a command from the client.
type CommandMsg struct {
	Cmd  string `json:"cmd"`
	Args string `json:"args,omitempty"`
}

// CopyModeInputMsg carries copy mode navigation from the client.
type CopyModeInputMsg struct {
	Action string `json:"action"`
	Count  int    `json:"count,omitempty"`
}

// MouseWheelMsg carries mouse wheel scroll events.
type MouseWheelMsg struct {
	Up    bool `json:"up"`    // true = scroll up, false = scroll down
	Lines int  `json:"lines"` // number of lines to scroll
}

// AttachMsg is the initial handshake from the client.
type AttachMsg struct {
	W int `json:"w"`
	H int `json:"h"`
}

// QueryMsg requests session info without attaching.
type QueryMsg struct {
	Type string `json:"type"` // "info", "tail", "send", "status"
	Args string `json:"args,omitempty"`
}

// QueryReplyMsg carries session info.
type QueryReplyMsg struct {
	Name       string     `json:"name"`
	Tabs       []string   `json:"tabs"`
	Clients    int        `json:"clients"`
	Ports      []PortInfo `json:"ports"`
	CreatedPID int        `json:"created_pid"`
}

// PortInfo in protocol for serialization.
type PortInfo struct {
	Port    int    `json:"port"`
	Process string `json:"process,omitempty"`
}

// --- Server → Client messages ---

// FrameMsg carries a pre-rendered ANSI frame.
type FrameMsg struct {
	Content string `json:"content"`
	W       int    `json:"w"`
	H       int    `json:"h"`
}

// SnapshotMsg carries full session state on attach.
type SnapshotMsg struct {
	Name      string   `json:"name"`
	Tabs      []string `json:"tabs"`
	ActiveTab int      `json:"active_tab"`
	Frame     string   `json:"frame"`
	W         int      `json:"w"`
	H         int      `json:"h"`
}

// SessionEventMsg carries session-level events.
type SessionEventMsg struct {
	Event string `json:"event"`
	Data  string `json:"data,omitempty"`
}

// CopyBufferMsg carries yanked text.
type CopyBufferMsg struct {
	Content string `json:"content"`
}

// ErrorMsg carries error feedback.
type ErrorMsg struct {
	Message string `json:"message"`
}

// --- Length-prefixed JSON codec ---
// Wire format: [4-byte big-endian length][JSON payload]
// Max message size: 16MB

const maxMsgSize = 16 * 1024 * 1024

// WriteMsg writes a length-prefixed JSON message to the writer.
// It is safe for concurrent use when each goroutine has its own writer,
// but callers must synchronize if sharing a writer.
func WriteMsg(w io.Writer, msgType string, data interface{}) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal data: %w", err)
	}

	env := Envelope{
		Type: msgType,
		Data: json.RawMessage(payload),
	}

	envBytes, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("marshal envelope: %w", err)
	}

	if len(envBytes) > maxMsgSize {
		return fmt.Errorf("message too large: %d bytes", len(envBytes))
	}

	header := make([]byte, 4)
	binary.BigEndian.PutUint32(header, uint32(len(envBytes)))

	if _, err := w.Write(header); err != nil {
		return fmt.Errorf("write header: %w", err)
	}
	if _, err := w.Write(envBytes); err != nil {
		return fmt.Errorf("write payload: %w", err)
	}

	return nil
}

// ReadMsg reads a length-prefixed JSON message from the reader.
func ReadMsg(r io.Reader) (*Envelope, error) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, err
	}

	length := binary.BigEndian.Uint32(header)
	if length > maxMsgSize {
		return nil, fmt.Errorf("message too large: %d bytes", length)
	}

	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, fmt.Errorf("read payload: %w", err)
	}

	var env Envelope
	if err := json.Unmarshal(payload, &env); err != nil {
		return nil, fmt.Errorf("unmarshal envelope: %w", err)
	}

	return &env, nil
}

// DecodeData unmarshals the Data field of an Envelope into the target.
func DecodeData(env *Envelope, target interface{}) error {
	return json.Unmarshal(env.Data, target)
}

// ConnWriter provides thread-safe writing to a connection.
type ConnWriter struct {
	w  io.Writer
	mu sync.Mutex
}

// NewConnWriter creates a new ConnWriter.
func NewConnWriter(w io.Writer) *ConnWriter {
	return &ConnWriter{w: w}
}

// WriteMsg writes a message with mutex protection.
func (cw *ConnWriter) WriteMsg(msgType string, data interface{}) error {
	cw.mu.Lock()
	defer cw.mu.Unlock()
	return WriteMsg(cw.w, msgType, data)
}
