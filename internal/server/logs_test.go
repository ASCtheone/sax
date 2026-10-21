//go:build !windows

package server

import (
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// logsClient runs a logs query against s over a pipe and collects what
// comes back, until log_end/error or the timeout.
type logsClient struct {
	t     *testing.T
	conn  net.Conn
	msgs  chan *Envelope
	start []string
	lines map[string][]string
	end   string
	err   string
}

func startLogs(t *testing.T, s *Server, req LogsRequest) *logsClient {
	t.Helper()
	local, remote := net.Pipe()
	args, _ := json.Marshal(req)
	go s.handleLogs(remote, string(args))
	c := &logsClient{t: t, conn: local, msgs: make(chan *Envelope, 64), lines: map[string][]string{}}
	t.Cleanup(func() { local.Close() })
	go func() {
		defer close(c.msgs)
		for {
			env, err := ReadMsg(local)
			if err != nil {
				return
			}
			c.msgs <- env
		}
	}()
	return c
}

// until reads messages until cond holds or the stream ends.
func (c *logsClient) until(cond func() bool) {
	c.t.Helper()
	deadline := time.After(5 * time.Second)
	for !cond() {
		select {
		case env, ok := <-c.msgs:
			if !ok {
				return
			}
			switch env.Type {
			case MsgLogStart:
				var m LogStartMsg
				_ = DecodeData(env, &m)
				c.start = m.Tabs
			case MsgLogLines:
				var m LogLinesMsg
				_ = DecodeData(env, &m)
				for _, l := range m.Lines {
					c.lines[m.Tab] = append(c.lines[m.Tab], ansi.Strip(l))
				}
			case MsgLogEnd:
				var m LogEndMsg
				_ = DecodeData(env, &m)
				c.end = m.Reason
				return
			case MsgError:
				var m ErrorMsg
				_ = DecodeData(env, &m)
				c.err = m.Message
				return
			}
		case <-deadline:
			c.t.Fatalf("timed out; lines so far: %v", c.lines)
		}
	}
}

func (c *logsClient) has(tab, text string) bool {
	for _, l := range c.lines[tab] {
		if strings.Contains(l, text) {
			return true
		}
	}
	return false
}

func logsServer(t *testing.T, name string) (*Server, *ManagedSession) {
	t.Helper()
	s := NewServer(name)
	s.InitTabs = []TabSpec{
		{Name: "api", Cmd: []string{"sh"}, Input: "for i in 1 2 3 4 5; do echo api-line-$i; done"},
		{Name: "web", Cmd: []string{"sh"}, Input: "echo web-line-1"},
	}
	ms, err := s.ensureSession(defaultCols, defaultRows)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Shutdown)
	waitRender(t, ms.paneFor("api"), "api-line-5")
	waitRender(t, ms.paneFor("web"), "web-line-1")
	return s, ms
}

func TestLogs_BacklogAndFilter(t *testing.T) {
	s, _ := logsServer(t, "logs-test")

	all := startLogs(t, s, LogsRequest{Lines: 100})
	all.until(func() bool { return false })
	if all.end != "done" || strings.Join(all.start, ",") != "api,web" {
		t.Fatalf("end=%q start=%v err=%q", all.end, all.start, all.err)
	}
	if !all.has("api", "api-line-1") || !all.has("api", "api-line-5") || !all.has("web", "web-line-1") {
		t.Errorf("backlog = %v", all.lines)
	}

	last := startLogs(t, s, LogsRequest{Tabs: []string{"api"}, Lines: 2})
	last.until(func() bool { return false })
	if len(last.lines["web"]) != 0 || len(last.lines["api"]) != 2 || !last.has("api", "api-line-5") {
		t.Errorf("-n 2 of api only = %v", last.lines)
	}

	missing := startLogs(t, s, LogsRequest{Tabs: []string{"ghost"}})
	missing.until(func() bool { return false })
	if !strings.Contains(missing.err, `no tab "ghost"`) {
		t.Errorf("missing tab err = %q", missing.err)
	}
}

func TestLogs_Follow(t *testing.T) {
	s, ms := logsServer(t, "logs-follow-test")

	f := startLogs(t, s, LogsRequest{Lines: 0, Follow: true})
	f.until(func() bool { return f.start != nil })
	if len(f.lines["api"]) != 0 {
		t.Errorf("-n 0 should send no backlog: %v", f.lines)
	}

	if _, err := ms.paneFor("web").Pty.WriteString("echo followed-$((40+2))\r"); err != nil {
		t.Fatal(err)
	}
	f.until(func() bool { return f.has("web", "followed-42") })

	// A tab added later (sax net up adding an app) is followed from its start.
	ms.WithLock(func() { _, _ = ms.addTab(TabSpec{Name: "worker", Cmd: []string{"sh"}, Input: "echo worker-hello"}) })
	f.until(func() bool { return f.has("worker", "worker-hello") })

	// Following one tab ends when that tab closes.
	one := startLogs(t, s, LogsRequest{Tabs: []string{"worker"}, Follow: true})
	one.until(func() bool { return one.start != nil })
	if _, err := ms.paneFor("worker").Pty.WriteString("exit\r"); err != nil {
		t.Fatal(err)
	}
	one.until(func() bool { return false })
	if one.end != "closed" {
		t.Errorf("end = %q, want closed", one.end)
	}
}
