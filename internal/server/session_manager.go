package server

import (
	"fmt"
	"sync"

	"github.com/asc/sax/internal/logger"
	"github.com/asc/sax/internal/monitor"
	"github.com/asc/sax/internal/porttracker"
	"github.com/asc/sax/internal/session"
	"github.com/asc/sax/internal/statusbar"
)

// ManagedSession wraps a session.Session with server-side management.
type ManagedSession struct {
	Name    string
	Session *session.Session
	Tracker *porttracker.Tracker
	Ports   []statusbar.PortInfo
	Logger  *logger.PaneLogger
	Monitor *monitor.Monitor

	clients   map[string]*ClientConn
	clientsMu sync.RWMutex

	copyBuffer string
	copyBufMu  sync.RWMutex

	alertCh chan monitor.Alert

	onPaneExit func(paneID string)
}

// SessionManager manages named sessions.
type SessionManager struct {
	sessions map[string]*ManagedSession
	mu       sync.RWMutex
}

// NewSessionManager creates a new session manager.
func NewSessionManager() *SessionManager {
	return &SessionManager{
		sessions: make(map[string]*ManagedSession),
	}
}

// Create creates a new named session. If cmdName is non-empty, the initial
// pane runs that command instead of the default shell.
func (sm *SessionManager) Create(name string, w, h int, cmdName string, cmdArgs []string) (*ManagedSession, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if _, exists := sm.sessions[name]; exists {
		return nil, fmt.Errorf("session %q already exists", name)
	}

	paneH := h - 2 // subtract tab bar + status bar
	var sess *session.Session
	var err error
	if cmdName != "" {
		sess, err = session.NewSessionWithCommand(w, paneH, cmdName, cmdArgs)
	} else {
		sess, err = session.NewSession(w, paneH)
	}
	if err != nil {
		return nil, fmt.Errorf("create session: %w", err)
	}

	alertCh := make(chan monitor.Alert, 16)
	ms := &ManagedSession{
		Name:    name,
		Session: sess,
		clients: make(map[string]*ClientConn),
		Logger:  logger.New(),
		Monitor: monitor.New(alertCh),
		alertCh: alertCh,
	}
	ms.Monitor.Start()

	// Process monitor alerts
	go func() {
		for alert := range ms.alertCh {
			ms.broadcastEvent("monitor_"+alert.Type, alert.PaneID)
		}
	}()

	// Start port tracker
	ms.Tracker = porttracker.New(func(ports []porttracker.ListeningPort) {
		var pi []statusbar.PortInfo
		for _, p := range ports {
			pi = append(pi, statusbar.PortInfo{Port: p.Port, Process: p.Process})
		}
		ms.Ports = pi
		ms.broadcastEvent(EventPortUpdate, "")
	})
	ms.Tracker.Start()

	sm.sessions[name] = ms

	// Start PTY readers for initial panes
	for _, pane := range sess.AllPanes() {
		ms.startPaneReader(pane)
	}

	return ms, nil
}

// Get returns a session by name.
func (sm *SessionManager) Get(name string) *ManagedSession {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return sm.sessions[name]
}

// Remove removes and closes a session.
func (sm *SessionManager) Remove(name string) {
	sm.mu.Lock()
	ms, exists := sm.sessions[name]
	if !exists {
		sm.mu.Unlock()
		return
	}
	delete(sm.sessions, name)
	sm.mu.Unlock()

	// Disconnect all clients
	ms.clientsMu.RLock()
	for _, cc := range ms.clients {
		cc.Close()
	}
	ms.clientsMu.RUnlock()

	if ms.Tracker != nil {
		ms.Tracker.Stop()
	}
	if ms.Monitor != nil {
		ms.Monitor.Stop()
	}
	if ms.Logger != nil {
		ms.Logger.Close()
	}
	ms.Session.Close()
}

// List returns all session names.
func (sm *SessionManager) List() []string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	names := make([]string, 0, len(sm.sessions))
	for name := range sm.sessions {
		names = append(names, name)
	}
	return names
}

// SessionCount returns the number of active sessions.
func (sm *SessionManager) SessionCount() int {
	sm.mu.RLock()
	defer sm.mu.RUnlock()
	return len(sm.sessions)
}

// CloseAll closes all sessions.
func (sm *SessionManager) CloseAll() {
	sm.mu.Lock()
	sessions := make(map[string]*ManagedSession)
	for k, v := range sm.sessions {
		sessions[k] = v
	}
	sm.sessions = make(map[string]*ManagedSession)
	sm.mu.Unlock()

	for _, ms := range sessions {
		ms.clientsMu.RLock()
		for _, cc := range ms.clients {
			cc.Close()
		}
		ms.clientsMu.RUnlock()

		if ms.Tracker != nil {
			ms.Tracker.Stop()
		}
		if ms.Monitor != nil {
			ms.Monitor.Stop()
		}
		if ms.Logger != nil {
			ms.Logger.Close()
		}
		ms.Session.Close()
	}
}

// --- ManagedSession methods ---

// AddClient registers a client connection.
func (ms *ManagedSession) AddClient(cc *ClientConn) {
	ms.clientsMu.Lock()
	ms.clients[cc.ID] = cc
	ms.clientsMu.Unlock()
	ms.recalcSize()
}

// RemoveClient unregisters a client connection.
func (ms *ManagedSession) RemoveClient(id string) {
	ms.clientsMu.Lock()
	delete(ms.clients, id)
	ms.clientsMu.Unlock()
	ms.recalcSize()
}

// ClientCount returns the number of connected clients.
func (ms *ManagedSession) ClientCount() int {
	ms.clientsMu.RLock()
	defer ms.clientsMu.RUnlock()
	return len(ms.clients)
}

// IsAttached returns true if at least one client is connected.
func (ms *ManagedSession) IsAttached() bool {
	return ms.ClientCount() > 0
}

// recalcSize recalculates session size as min of all client sizes.
func (ms *ManagedSession) recalcSize() {
	ms.clientsMu.RLock()
	defer ms.clientsMu.RUnlock()

	if len(ms.clients) == 0 {
		return
	}

	minW, minH := 0, 0
	for _, cc := range ms.clients {
		w, h := cc.Size()
		if minW == 0 || w < minW {
			minW = w
		}
		if minH == 0 || h < minH {
			minH = h
		}
	}

	if minW > 0 && minH > 0 {
		ms.Session.Resize(minW, minH)
	}
}

// MarkDirty marks all clients as needing a frame update.
func (ms *ManagedSession) MarkDirty() {
	ms.clientsMu.RLock()
	defer ms.clientsMu.RUnlock()
	for _, cc := range ms.clients {
		cc.MarkDirty()
	}
}

// broadcastEvent sends a session event to all clients.
func (ms *ManagedSession) broadcastEvent(event, data string) {
	ms.clientsMu.RLock()
	defer ms.clientsMu.RUnlock()
	for _, cc := range ms.clients {
		_ = cc.writer.WriteMsg(MsgSessionEvent, &SessionEventMsg{
			Event: event,
			Data:  data,
		})
	}
	// Also mark dirty to trigger re-render
	for _, cc := range ms.clients {
		cc.MarkDirty()
	}
}

// startPaneReader starts a goroutine to read PTY output for a pane.
func (ms *ManagedSession) startPaneReader(pane *session.Pane) {
	go func() {
		buf := make([]byte, 32*1024)
		for {
			n, err := pane.Pty.Read(buf)
			if n > 0 {
				data := make([]byte, n)
				copy(data, buf[:n])
				_, _ = pane.Term.Write(data)

				// Feed to port tracker
				if ms.Tracker != nil {
					ms.Tracker.FeedOutput(data)
				}

				// Feed to logger
				if ms.Logger != nil {
					ms.Logger.Write(pane.ID, data)
				}

				// Notify monitor (check if pane is in background)
				if ms.Monitor != nil {
					activePane := ms.Session.ActivePane()
					isBackground := activePane == nil || activePane.ID != pane.ID
					ms.Monitor.NotifyActivity(pane.ID, isBackground)
				}

				// Mark all clients dirty
				ms.MarkDirty()
			}
			if err != nil {
				pane.HasExited = true
				pane.ExitErr = err
				if ms.onPaneExit != nil {
					ms.onPaneExit(pane.ID)
				}
				return
			}
		}
	}()
}

// SetCopyBuffer stores yanked text.
func (ms *ManagedSession) SetCopyBuffer(content string) {
	ms.copyBufMu.Lock()
	ms.copyBuffer = content
	ms.copyBufMu.Unlock()
}

// GetCopyBuffer returns the copy buffer content.
func (ms *ManagedSession) GetCopyBuffer() string {
	ms.copyBufMu.RLock()
	defer ms.copyBufMu.RUnlock()
	return ms.copyBuffer
}
