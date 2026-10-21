package ipc

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net"
)

// LogsRequest asks a session for its tabs' output as lines (see
// server.LogsRequest, which this mirrors).
type LogsRequest struct {
	Tabs   []string `json:"tabs,omitempty"`
	Lines  int      `json:"lines"`
	Follow bool     `json:"follow"`
}

// LogLines is a batch of one tab's lines. Skipped counts lines lost to
// scrollback overflow before them.
type LogLines struct {
	Tab     string   `json:"tab"`
	Lines   []string `json:"lines"`
	Skipped int64    `json:"skipped,omitempty"`
}

// ErrLogsUnsupported means the session runs a sax without the logs query.
var ErrLogsUnsupported = errors.New("session cannot stream logs")

const maxLogMsg = 16 << 20

// Logs streams a session's output. onStart gets the tab names, onLines each
// batch (an error from it stops the stream). It returns why the stream
// ended ("done", "closed", "session ended").
func Logs(session string, req LogsRequest, onStart func([]string), onLines func(LogLines) error) (string, error) {
	conn, err := Dial(session)
	if err != nil {
		return "", fmt.Errorf("session %q not found", session)
	}
	defer conn.Close()

	args, err := json.Marshal(req)
	if err != nil {
		return "", err
	}
	if err := writeEnvelope(conn, "query", map[string]string{"type": "logs", "args": string(args)}); err != nil {
		return "", fmt.Errorf("send logs query: %w", err)
	}

	for {
		var env struct {
			Type string          `json:"type"`
			Data json.RawMessage `json:"data"`
		}
		if err := readEnvelope(conn, &env); err != nil {
			return "", fmt.Errorf("session %q: %w", session, err)
		}
		switch env.Type {
		case "log_start":
			var m struct {
				Tabs []string `json:"tabs"`
			}
			if err := json.Unmarshal(env.Data, &m); err != nil {
				return "", err
			}
			onStart(m.Tabs)
		case "log_lines":
			var m LogLines
			if err := json.Unmarshal(env.Data, &m); err != nil {
				return "", err
			}
			if err := onLines(m); err != nil {
				return "", err
			}
		case "log_end":
			var m struct {
				Reason string `json:"reason"`
			}
			_ = json.Unmarshal(env.Data, &m)
			return m.Reason, nil
		case "error":
			var m struct {
				Message string `json:"message"`
			}
			_ = json.Unmarshal(env.Data, &m)
			return "", errors.New(m.Message)
		case "query_reply":
			return "", ErrLogsUnsupported // an older daemon answered with session info
		}
	}
}

func writeEnvelope(conn net.Conn, msgType string, data interface{}) error {
	payload, err := json.Marshal(data)
	if err != nil {
		return err
	}
	env, err := json.Marshal(map[string]interface{}{"type": msgType, "data": json.RawMessage(payload)})
	if err != nil {
		return err
	}
	frame := make([]byte, 4+len(env))
	binary.BigEndian.PutUint32(frame, uint32(len(env)))
	copy(frame[4:], env)
	_, err = conn.Write(frame)
	return err
}

func readEnvelope(conn net.Conn, v interface{}) error {
	header := make([]byte, 4)
	if _, err := readFull(conn, header); err != nil {
		return err
	}
	n := binary.BigEndian.Uint32(header)
	if n == 0 || n > maxLogMsg {
		return fmt.Errorf("bad message size %d", n)
	}
	data := make([]byte, n)
	if _, err := readFull(conn, data); err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}
