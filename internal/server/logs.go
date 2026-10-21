package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"sort"
	"time"

	"github.com/asc/sax/internal/scrollback"
)

// Logs: `sax net logs` reads tabs' output as lines, compose style. Every
// pane's output already goes through its scrollback buffer, which assembles
// lines (collapsing \r progress updates) and numbers them, so a follower
// only remembers the next line number per pane and polls for new ones. The
// PTY readers are not involved and a slow reader cannot stall an app.
//
// A logs query gets: log_start (the tabs), then log_lines messages, then
// log_end, or an error. Older daemons answer with a plain query_reply.

const (
	QueryLogs   = "logs"
	MsgLogStart = "log_start"
	MsgLogLines = "log_lines"
	MsgLogEnd   = "log_end"

	logPollInterval = 100 * time.Millisecond
	logWriteTimeout = 5 * time.Second
	logBatchLines   = 1000
)

// LogsRequest is the JSON argument of a logs query.
type LogsRequest struct {
	Tabs   []string `json:"tabs,omitempty"` // empty = every tab, including ones added later
	Lines  int      `json:"lines"`          // backlog per pane
	Follow bool     `json:"follow"`
}

// LogStartMsg lists the tabs being read.
type LogStartMsg struct {
	Tabs []string `json:"tabs"`
}

// LogLinesMsg carries a tab's new lines (with their SGR colors). Skipped
// counts lines that left the scrollback before they could be sent.
type LogLinesMsg struct {
	Tab     string   `json:"tab"`
	Lines   []string `json:"lines"`
	Skipped int64    `json:"skipped,omitempty"`
}

// LogEndMsg ends a logs stream.
type LogEndMsg struct {
	Reason string `json:"reason"`
}

type logCursor struct {
	tab  string
	buf  *scrollback.Buffer
	next int64 // serial of the next line to send
}

// read returns the lines written since the last read.
func (c *logCursor) read() ([]string, int64) {
	end, oldest := c.buf.TotalWritten(), c.buf.OldestSerial()
	var skipped int64
	if c.next < oldest {
		skipped, c.next = oldest-c.next, oldest
	}
	var lines []string
	for ; c.next < end; c.next++ {
		if line, ok := c.buf.LineAtSerial(c.next); ok {
			lines = append(lines, line)
		}
	}
	return lines, skipped
}

// logFollower tracks the panes of the requested tabs.
type logFollower struct {
	ms      *ManagedSession
	req     LogsRequest
	cursors map[string]*logCursor // by pane ID
	order   []string              // pane IDs in tab order
}

type tabPane struct {
	tab, id string
	buf     *scrollback.Buffer
}

// panes lists the panes of the wanted tabs in tab order, and the wanted
// tabs that do not exist.
func (f *logFollower) panes() ([]tabPane, []string) {
	f.ms.sessMu.RLock()
	defer f.ms.sessMu.RUnlock()
	var out []tabPane
	seen := map[string]bool{}
	for _, tab := range f.ms.Session.Tabs {
		if len(f.req.Tabs) > 0 && !contains(f.req.Tabs, tab.Name) {
			continue
		}
		seen[tab.Name] = true
		ids := make([]string, 0, len(tab.Panes))
		for id := range tab.Panes {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			out = append(out, tabPane{tab: tab.Name, id: id, buf: tab.Panes[id].Term.Scrollback})
		}
	}
	var missing []string
	for _, name := range f.req.Tabs {
		if !seen[name] {
			missing = append(missing, name)
		}
	}
	return out, missing
}

// start takes the current panes, positioned req.Lines lines back.
func (f *logFollower) start() ([]string, error) {
	panes, missing := f.panes()
	if len(missing) > 0 {
		return nil, fmt.Errorf("no tab %q in session %q", missing[0], f.ms.Name)
	}
	f.cursors = map[string]*logCursor{}
	var tabs []string
	for _, p := range panes {
		next := p.buf.TotalWritten() - int64(f.req.Lines)
		if oldest := p.buf.OldestSerial(); next < oldest {
			next = oldest
		}
		f.cursors[p.id] = &logCursor{tab: p.tab, buf: p.buf, next: next}
		f.order = append(f.order, p.id)
		if len(tabs) == 0 || tabs[len(tabs)-1] != p.tab {
			tabs = append(tabs, p.tab)
		}
	}
	return tabs, nil
}

// poll sends every pane's new lines. Panes that appeared since the last poll
// (a tab added by sax net up) are read from their first line; panes that
// closed are drained and dropped. It reports false once nothing is left to
// follow among explicitly requested tabs.
func (f *logFollower) poll(send func(LogLinesMsg) error) (bool, error) {
	panes, _ := f.panes()
	live := map[string]bool{}
	var order []string
	for _, p := range panes {
		live[p.id] = true
		order = append(order, p.id)
		if _, ok := f.cursors[p.id]; !ok {
			f.cursors[p.id] = &logCursor{tab: p.tab, buf: p.buf, next: p.buf.OldestSerial()}
		}
	}
	for _, id := range f.order { // closed panes last, after a final read
		if !live[id] {
			order = append(order, id)
		}
	}
	for _, id := range order {
		c := f.cursors[id]
		lines, skipped := c.read()
		for len(lines) > 0 || skipped > 0 {
			n := min(len(lines), logBatchLines)
			if err := send(LogLinesMsg{Tab: c.tab, Lines: lines[:n], Skipped: skipped}); err != nil {
				return false, err
			}
			lines, skipped = lines[n:], 0
		}
		if !live[id] {
			delete(f.cursors, id)
		}
	}
	f.order = order[:len(panes)]
	return len(f.req.Tabs) == 0 || len(panes) > 0, nil
}

// handleLogs serves a logs query until the client leaves, the requested
// tabs close or the server shuts down.
func (s *Server) handleLogs(conn net.Conn, args string) {
	defer conn.Close()
	write := func(msgType string, data interface{}) error {
		_ = conn.SetWriteDeadline(time.Now().Add(logWriteTimeout))
		return WriteMsg(conn, msgType, data)
	}

	var req LogsRequest
	if err := json.Unmarshal([]byte(args), &req); err != nil {
		_ = write(MsgError, &ErrorMsg{Message: "logs: " + err.Error()})
		return
	}
	req.Lines = max(0, min(req.Lines, scrollback.ConfiguredCapacity()))
	ms := s.sessions.Get(s.name)
	if ms == nil {
		_ = write(MsgError, &ErrorMsg{Message: "session not started"})
		return
	}

	f := &logFollower{ms: ms, req: req}
	tabs, err := f.start()
	if err != nil {
		_ = write(MsgError, &ErrorMsg{Message: err.Error()})
		return
	}
	if write(MsgLogStart, &LogStartMsg{Tabs: tabs}) != nil {
		return
	}
	send := func(m LogLinesMsg) error { return write(MsgLogLines, &m) }
	if _, err := f.poll(send); err != nil {
		return
	}
	if !req.Follow {
		_ = write(MsgLogEnd, &LogEndMsg{Reason: "done"})
		return
	}

	gone := make(chan struct{}) // the client hung up
	go func() {
		_, _ = io.Copy(io.Discard, conn)
		close(gone)
	}()
	ticker := time.NewTicker(logPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.done:
			_ = write(MsgLogEnd, &LogEndMsg{Reason: "session ended"})
			return
		case <-gone:
			return
		case <-ticker.C:
			more, err := f.poll(send)
			if err != nil {
				return
			}
			if !more {
				_ = write(MsgLogEnd, &LogEndMsg{Reason: "closed"})
				return
			}
		}
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
