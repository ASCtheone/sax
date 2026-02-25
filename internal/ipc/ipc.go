package ipc

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// SessionInfo describes a discovered session.
type SessionInfo struct {
	Name      string
	Path      string
	Attached  bool
	CreatedAt time.Time
}

// SessionsDir returns the directory where session sockets live.
func SessionsDir() string {
	switch runtime.GOOS {
	case "windows":
		// On Windows we use named pipes, but still need a directory for metadata
		appData := os.Getenv("APPDATA")
		if appData == "" {
			appData = filepath.Join(os.Getenv("USERPROFILE"), "AppData", "Roaming")
		}
		return filepath.Join(appData, "sax", "sessions")
	default:
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".sax", "sessions")
	}
}

// LogsDir returns the directory for pane output logs.
func LogsDir() string {
	switch runtime.GOOS {
	case "windows":
		appData := os.Getenv("APPDATA")
		if appData == "" {
			appData = filepath.Join(os.Getenv("USERPROFILE"), "AppData", "Roaming")
		}
		return filepath.Join(appData, "sax", "logs")
	default:
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".sax", "logs")
	}
}

// HardcopyDir returns the directory for hardcopy dumps.
func HardcopyDir() string {
	switch runtime.GOOS {
	case "windows":
		appData := os.Getenv("APPDATA")
		if appData == "" {
			appData = filepath.Join(os.Getenv("USERPROFILE"), "AppData", "Roaming")
		}
		return filepath.Join(appData, "sax", "hardcopy")
	default:
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".sax", "hardcopy")
	}
}

// SocketPath returns the socket/pipe path for a named session.
func SocketPath(name string) string {
	if runtime.GOOS == "windows" {
		return `\\.\pipe\sax-` + name
	}
	return filepath.Join(SessionsDir(), name+".sock")
}

// PidPath returns the path to the PID file for a named session.
func PidPath(name string) string {
	return filepath.Join(SessionsDir(), name+".pid")
}

// EnsureDirs creates all required directories.
func EnsureDirs() error {
	for _, dir := range []string{SessionsDir(), LogsDir(), HardcopyDir()} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}
	return nil
}

// Listen creates a listener for the named session.
func Listen(name string) (net.Listener, error) {
	if err := EnsureDirs(); err != nil {
		return nil, err
	}
	return platformListen(name)
}

// Dial connects to the named session.
func Dial(name string) (net.Conn, error) {
	return platformDial(name)
}

// ListSessions returns all active sessions.
func ListSessions() ([]SessionInfo, error) {
	if err := EnsureDirs(); err != nil {
		return nil, err
	}
	return platformListSessions()
}

// CleanupSocket removes the socket file for a session.
func CleanupSocket(name string) {
	platformCleanupSocket(name)
}

// IsSessionAlive probes whether a session socket is responsive.
func IsSessionAlive(name string) bool {
	conn, err := Dial(name)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// CleanStaleSessions removes sockets for dead sessions.
func CleanStaleSessions() {
	sessions, err := ListSessions()
	if err != nil {
		return
	}
	for _, s := range sessions {
		if !IsSessionAlive(s.Name) {
			CleanupSocket(s.Name)
			// Remove PID file too
			os.Remove(PidPath(s.Name))
		}
	}
}

// DefaultSessionName returns a suitable default name.
func DefaultSessionName() string {
	return "default"
}

// ValidateSessionName checks if a session name is valid.
func ValidateSessionName(name string) error {
	if name == "" {
		return fmt.Errorf("session name cannot be empty")
	}
	if len(name) > 64 {
		return fmt.Errorf("session name too long (max 64 chars)")
	}
	for _, c := range name {
		if !isValidNameChar(c) {
			return fmt.Errorf("invalid character in session name: %c", c)
		}
	}
	if strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("session name cannot contain path separators")
	}
	return nil
}

// QuerySession connects to a session and retrieves its info.
// Returns nil if the session is not alive.
func QuerySession(name string) map[string]interface{} {
	conn, err := Dial(name)
	if err != nil {
		return nil
	}
	defer conn.Close()

	// Send a query message — the server protocol package handles this,
	// but we do raw JSON here to avoid a circular import.
	// Wire format: 4-byte length + JSON envelope
	queryPayload := []byte(`{"type":"query","data":{}}`)
	header := make([]byte, 4)
	header[0] = byte(len(queryPayload) >> 24)
	header[1] = byte(len(queryPayload) >> 16)
	header[2] = byte(len(queryPayload) >> 8)
	header[3] = byte(len(queryPayload))
	conn.Write(header)
	conn.Write(queryPayload)

	// Read reply — 4-byte length + JSON
	replyHeader := make([]byte, 4)
	if _, err := readFull(conn, replyHeader); err != nil {
		return nil
	}
	length := int(replyHeader[0])<<24 | int(replyHeader[1])<<16 | int(replyHeader[2])<<8 | int(replyHeader[3])
	if length <= 0 || length > 1024*1024 {
		return nil
	}
	replyData := make([]byte, length)
	if _, err := readFull(conn, replyData); err != nil {
		return nil
	}

	// Parse just enough to extract the data field
	var envelope struct {
		Type string                 `json:"type"`
		Data map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(replyData, &envelope); err != nil {
		return nil
	}
	return envelope.Data
}

func readFull(conn net.Conn, buf []byte) (int, error) {
	total := 0
	for total < len(buf) {
		n, err := conn.Read(buf[total:])
		total += n
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

// QuerySessionTyped connects to a session and sends a typed query.
// Returns the reply data as a map, or nil on failure.
func QuerySessionTyped(name, queryType, args string) map[string]interface{} {
	conn, err := Dial(name)
	if err != nil {
		return nil
	}
	defer conn.Close()

	queryPayload, _ := json.Marshal(map[string]interface{}{
		"type": queryType,
		"args": args,
	})
	envelope, _ := json.Marshal(map[string]interface{}{
		"type": "query",
		"data": json.RawMessage(queryPayload),
	})

	header := make([]byte, 4)
	header[0] = byte(len(envelope) >> 24)
	header[1] = byte(len(envelope) >> 16)
	header[2] = byte(len(envelope) >> 8)
	header[3] = byte(len(envelope))
	conn.Write(header)
	conn.Write(envelope)

	// Read reply
	replyHeader := make([]byte, 4)
	if _, err := readFull(conn, replyHeader); err != nil {
		return nil
	}
	length := int(replyHeader[0])<<24 | int(replyHeader[1])<<16 | int(replyHeader[2])<<8 | int(replyHeader[3])
	if length <= 0 || length > 1024*1024 {
		return nil
	}
	replyData := make([]byte, length)
	if _, err := readFull(conn, replyData); err != nil {
		return nil
	}

	var env struct {
		Type string                 `json:"type"`
		Data map[string]interface{} `json:"data"`
	}
	if err := json.Unmarshal(replyData, &env); err != nil {
		return nil
	}
	return env.Data
}

func isValidNameChar(c rune) bool {
	return (c >= 'a' && c <= 'z') ||
		(c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9') ||
		c == '-' || c == '_' || c == '.'
}
