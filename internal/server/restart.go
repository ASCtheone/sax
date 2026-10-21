package server

import (
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/asc/sax/internal/session"
)

// QueryReplaceTab restarts a tab (sax net restart): a new tab from a
// TabSpec takes the old one's place, the old tab's processes are stopped,
// and only then is the spec's Input (the app command) typed in, so the new
// app never races the old one for its port. Older daemons answer with a
// plain info reply.
const QueryReplaceTab = "replacetab"

// killGrace is how long stopped processes get to exit before SIGKILL.
const killGrace = 5 * time.Second

// handleReplaceTab serves a replacetab query. It does not hold the session
// lock while waiting for the old processes.
func (s *Server) handleReplaceTab(conn net.Conn, args string) {
	defer conn.Close()
	reply := map[string]interface{}{"ok": false}
	defer func() { _ = WriteMsg(conn, MsgQueryReply, &reply) }()

	var spec TabSpec
	if err := json.Unmarshal([]byte(args), &spec); err != nil {
		reply["error"] = err.Error()
		return
	}
	ms := s.sessions.Get(s.name)
	if ms == nil {
		reply["error"] = "session not started"
		return
	}
	killed, err := ms.replaceTab(spec)
	if err != nil {
		reply["error"] = err.Error()
		return
	}
	reply["ok"], reply["killed"] = true, killed
}

// replaceTab swaps in a new tab for spec.Name, stops the old tab's process
// tree, closes it, then types spec.Input into the new tab.
func (ms *ManagedSession) replaceTab(spec TabSpec) (int, error) {
	if len(spec.Cmd) == 0 {
		return 0, fmt.Errorf("tab %s: empty command", spec.Name)
	}

	var old, tab *session.Tab
	var roots []int
	var err error
	ms.WithLock(func() {
		if old = ms.Session.TabByName(spec.Name); old == nil {
			err = fmt.Errorf("no tab %q in session %q", spec.Name, ms.Name)
			return
		}
		if tab, err = ms.Session.ReplaceTab(old, spec.Name, spec.Cmd[0], spec.Cmd[1:], spec.Dir, spec.Env); err != nil {
			return
		}
		for _, pane := range tab.Panes {
			ms.startPaneReader(pane)
			ms.applyNewPaneHooks(pane)
		}
		ms.repin(old, tab)
		for _, pane := range old.Panes {
			if pid := pane.Pty.Pid(); pid > 0 {
				roots = append(roots, pid)
			}
		}
		ms.MarkDirty()
	})
	if err != nil {
		return 0, err
	}

	// The old tab is out of the session, so its shell exiting is a no-op
	// for the pane-exit handler (it cannot end the session).
	killed := killTree(roots, killGrace)
	old.Close()

	ms.WithLock(func() { ms.typeInput(tab.ActivePaneObj(), spec.Input) })
	return killed, nil
}

// repin moves clients pinned to old (sax -a router.app) to its replacement.
// Callers hold the session lock.
func (ms *ManagedSession) repin(old, tab *session.Tab) {
	ms.clientsMu.RLock()
	defer ms.clientsMu.RUnlock()
	for _, cc := range ms.clients {
		if cc.pinned == old {
			cc.pinned = tab
		}
	}
}
