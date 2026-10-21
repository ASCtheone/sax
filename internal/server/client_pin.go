package server

import (
	"github.com/asc/sax/internal/session"
)

// A client attached with AttachMsg.Tab is pinned to that tab: `sax -a v1.api`
// shows only the api app of router v1 while `sax -a v1` shows them all. The
// pin is per client, so the session's active tab (what group clients see)
// is not touched, and tab commands (new, close, next, prev, go to, window
// list) are ignored for it.

// pin pins the client to the named tab. It reports false when there is no
// such tab.
func (cc *ClientConn) pin(name string) bool {
	tab := cc.ms.Session.TabByName(name)
	if tab == nil {
		return false
	}
	cc.pinned = tab
	return true
}

// tab returns the tab the client sees: its pinned tab, or the session's
// active one. nil when the pinned tab is gone. Callers hold the session lock.
func (cc *ClientConn) tab() *session.Tab {
	if cc.pinned == nil {
		return cc.ms.Session.CurrentTab()
	}
	for _, t := range cc.ms.Session.Tabs {
		if t == cc.pinned {
			return t
		}
	}
	return nil
}

// pane returns the pane the client types into.
func (cc *ClientConn) pane() *session.Pane {
	tab := cc.tab()
	if tab == nil {
		return nil
	}
	return tab.ActivePaneObj()
}

// pinGone reports whether the client's pinned tab has closed (its app
// exited or was removed), so the client should be detached.
func (cc *ClientConn) pinGone() bool {
	return cc.pinned != nil && cc.tab() == nil
}

// tabCommand reports whether cmd switches, adds or closes tabs, which a
// pinned client may not do.
func tabCommand(cmd string) bool {
	switch cmd {
	case CmdCreateTab, CmdCloseTab, CmdNextTab, CmdPrevTab, CmdGoToTab, CmdWindowList:
		return true
	}
	return false
}

// tabIndex returns the index of tab in the session, or -1.
func tabIndex(sess *session.Session, tab *session.Tab) int {
	for i, t := range sess.Tabs {
		if t == tab {
			return i
		}
	}
	return -1
}
