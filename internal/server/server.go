package server

import (
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/asc/sax/internal/ipc"
	"github.com/asc/sax/internal/saxrc"
)

// Server is the SAX daemon that owns sessions and accepts client connections.
type Server struct {
	name     string
	listener net.Listener
	sessions *SessionManager
	done     chan struct{}
	wg       sync.WaitGroup
	createMu sync.Mutex // serializes ensureSession

	// Initial command to run instead of default shell (empty = shell)
	InitCmd     string
	InitCmdArgs []string
	InitWorkDir string

	// InitTabs, when set, replaces InitCmd: the session starts with one named
	// tab per command (sax net up's view of a router).
	InitTabs []TabSpec

	// LazyStart defers creating the session (and running its command) until
	// the first client attaches; otherwise it starts with the daemon.
	LazyStart bool

	// Saxrc is the parsed ~/.saxrc, consulted for startup hooks and run
	// commands when sessions are created. Never nil after doServerMode wiring.
	Saxrc *saxrc.Config
}

// Size used for a session started before any client attaches; the first
// attach resizes it to the client's terminal.
const (
	defaultCols = 120
	defaultRows = 40
)

// NewServer creates a new server for the named session.
func NewServer(name string) *Server {
	return &Server{
		name:     name,
		sessions: NewSessionManager(),
		done:     make(chan struct{}),
	}
}

// Run starts the server: listens on the session socket and accepts clients.
func (s *Server) Run() error {
	if err := ipc.EnsureDirs(); err != nil {
		return fmt.Errorf("ensure dirs: %w", err)
	}

	// Clean stale sessions first
	ipc.CleanStaleSessions()

	// Apply ~/.saxrc startup hooks and run commands to sessions as they spawn.
	if s.Saxrc != nil {
		s.sessions.ConfigureHooks(s.Saxrc.Hooks["new-pane"], s.Saxrc.RunCmds)
	}

	listener, err := ipc.Listen(s.name)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	s.listener = listener

	// Write PID file
	pidPath := ipc.PidPath(s.name)
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
		log.Printf("warning: could not write PID file: %v", err)
	}

	// Handle signals for graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		select {
		case <-sigCh:
			s.Shutdown()
		case <-s.done:
		}
	}()

	log.Printf("sax server %q listening (pid %d)", s.name, os.Getpid())

	// Start the session now so a detached command (sax -x, sax net up, MCP
	// sax_exec) runs immediately instead of waiting for the first attach.
	if !s.LazyStart {
		if _, err := s.ensureSession(defaultCols, defaultRows); err != nil {
			log.Printf("failed to start session: %v", err)
		}
	}

	// Accept loop
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			select {
			case <-s.done:
				return nil
			default:
				log.Printf("accept error: %v", err)
				continue
			}
		}

		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.handleClient(conn)
		}()
	}
}

// handleClient processes a new client connection.
func (s *Server) handleClient(conn net.Conn) {
	// Read the initial attach message to get dimensions
	env, err := ReadMsg(conn)
	if err != nil {
		log.Printf("failed to read attach message: %v", err)
		conn.Close()
		return
	}

	// Handle query (non-attaching info request)
	if env.Type == MsgQuery {
		var query QueryMsg
		_ = DecodeData(env, &query)
		s.handleQuery(conn, query)
		return
	}

	if env.Type != MsgAttach {
		log.Printf("expected attach message, got %q", env.Type)
		conn.Close()
		return
	}

	var attach AttachMsg
	if err := DecodeData(env, &attach); err != nil {
		log.Printf("failed to decode attach: %v", err)
		conn.Close()
		return
	}

	// Get or create the managed session
	ms, err := s.ensureSession(attach.W, attach.H)
	if err != nil {
		log.Printf("failed to create session: %v", err)
		_ = WriteMsg(conn, MsgError, &ErrorMsg{Message: err.Error()})
		conn.Close()
		return
	}

	// Create client connection
	cc := newClientConn(conn, ms, s)
	ms.AddClient(cc)

	// Send initial snapshot
	cc.handleAttach(attach)

	// Start read/render loops (blocks until client disconnects)
	cc.Run()

	// Wait for client to finish
	<-cc.done

	log.Printf("client %s disconnected from session %q", cc.ID, s.name)

	// If no more clients and no sessions, server could shut down
	// But we keep running to allow reattach
}

// ensureSession returns the managed session, creating it at w x h (and
// starting its initial command) if it does not exist yet.
func (s *Server) ensureSession(w, h int) (*ManagedSession, error) {
	s.createMu.Lock()
	defer s.createMu.Unlock()

	if ms := s.sessions.Get(s.name); ms != nil {
		return ms, nil
	}
	if len(s.InitTabs) == 0 {
		return s.sessions.Create(s.name, w, h, s.InitCmd, s.InitCmdArgs, s.InitWorkDir, s.handlePaneExit)
	}

	first := s.InitTabs[0]
	if len(first.Cmd) == 0 {
		return nil, fmt.Errorf("tab %s: empty command", first.Name)
	}
	ms, err := s.sessions.Create(s.name, w, h, first.Cmd[0], first.Cmd[1:], first.Dir, s.handlePaneExit,
		s.InitTabs[1:]...)
	if err != nil {
		return nil, err
	}
	ms.Session.Tabs[0].Name = first.Name
	return ms, nil
}

// handleQuery responds to a session info query and closes the connection.
func (s *Server) handleQuery(conn net.Conn, query QueryMsg) {
	defer conn.Close()

	ms := s.sessions.Get(s.name)

	switch query.Type {
	case "tail":
		s.handleQueryTail(conn, ms, query.Args)
	case "send":
		s.handleQuerySend(conn, ms, query.Args)
	case "status":
		s.handleQueryStatus(conn, ms)
	default:
		// Default info query
		reply := QueryReplyMsg{
			Name:       s.name,
			CreatedPID: os.Getpid(),
		}
		if ms != nil {
			reply.Tabs = ms.Session.TabNames()
			reply.Clients = ms.ClientCount()
			for _, p := range ms.Ports {
				reply.Ports = append(reply.Ports, PortInfo{Port: p.Port, Process: p.Process})
			}
		}
		_ = WriteMsg(conn, MsgQueryReply, &reply)
	}
}

// handleQueryTail returns the last N lines from the active pane.
func (s *Server) handleQueryTail(conn net.Conn, ms *ManagedSession, args string) {
	n := 10
	if args != "" {
		fmt.Sscanf(args, "%d", &n)
	}
	if n < 1 {
		n = 1
	}
	if n > 1000 {
		n = 1000
	}

	content := ""
	if ms != nil {
		if pane := ms.Session.ActivePane(); pane != nil {
			content = tailLines(pane.Render(), n)
		}
	}

	_ = WriteMsg(conn, MsgQueryReply, &map[string]string{"tail": content})
}

// tailLines returns the last n lines of a rendered pane, ignoring the blank
// rows below the output (a fresh 40-row pane is mostly empty, and counting
// those rows would hide short output from `sax --tail` and sax_tail).
func tailLines(rendered string, n int) string {
	lines := strings.Split(rendered, "\n")
	end := len(lines)
	for end > 0 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	start := end - n
	if start < 0 {
		start = 0
	}
	return strings.Join(lines[start:end], "\n")
}

// handleQuerySend writes text into the active pane's PTY.
func (s *Server) handleQuerySend(conn net.Conn, ms *ManagedSession, text string) {
	ok := false
	if ms != nil {
		pane := ms.Session.ActivePane()
		if pane != nil && !pane.HasExited {
			_, err := pane.Pty.Write([]byte(text))
			ok = err == nil
		}
	}
	_ = WriteMsg(conn, MsgQueryReply, &map[string]bool{"ok": ok})
}

// handleQueryStatus returns full session status as structured data.
func (s *Server) handleQueryStatus(conn net.Conn, ms *ManagedSession) {
	status := map[string]interface{}{
		"name":    s.name,
		"pid":     os.Getpid(),
		"alive":   ms != nil,
	}

	if ms != nil {
		status["clients"] = ms.ClientCount()
		status["tabs"] = len(ms.Session.Tabs)
		status["active_tab"] = ms.Session.ActiveTab

		var tabDetails []map[string]interface{}
		for i, tab := range ms.Session.Tabs {
			td := map[string]interface{}{
				"index": i,
				"name":  tab.Name,
				"panes": tab.Layout.PaneCount(),
			}
			tabDetails = append(tabDetails, td)
		}
		status["tab_details"] = tabDetails

		var ports []map[string]interface{}
		for _, p := range ms.Ports {
			ports = append(ports, map[string]interface{}{
				"port":    p.Port,
				"process": p.Process,
			})
		}
		status["ports"] = ports

		// Active pane info
		pane := ms.Session.ActivePane()
		if pane != nil {
			status["active_pane"] = map[string]interface{}{
				"id":       pane.ID,
				"exited":   pane.HasExited,
			}
		}
	}

	_ = WriteMsg(conn, MsgQueryReply, &status)
}

// handlePaneExit processes a pane exit event.
func (s *Server) handlePaneExit(paneID string) {
	ms := s.sessions.Get(s.name)
	if ms == nil {
		return
	}

	sess := ms.Session
	tabIdx := sess.FindPaneTab(paneID)
	if tabIdx < 0 {
		return
	}

	tab := sess.Tabs[tabIdx]
	alive := tab.RemovePane(paneID)
	if !alive {
		sess.RemoveTab(tabIdx)
		if len(sess.Tabs) == 0 {
			// All panes closed — shut down
			ms.broadcastEvent(EventSessionClosed, "")
			s.Shutdown()
			return
		}
	}
	ms.MarkDirty()
}

// Shutdown gracefully stops the server.
func (s *Server) Shutdown() {
	select {
	case <-s.done:
		return // already shutting down
	default:
	}
	close(s.done)

	if s.listener != nil {
		s.listener.Close()
	}

	s.sessions.CloseAll()

	// Cleanup socket and PID file
	ipc.CleanupSocket(s.name)
	os.Remove(ipc.PidPath(s.name))

	log.Printf("sax server %q shut down", s.name)
}

// WaitForShutdown blocks until the server is done.
func (s *Server) WaitForShutdown() {
	<-s.done
	s.wg.Wait()
}
