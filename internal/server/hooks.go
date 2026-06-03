package server

import (
	"fmt"
	"strings"
	"time"

	"github.com/asc/sax/internal/session"
)

// newPaneHookDelay gives a freshly spawned shell time to print its prompt
// before hook commands are typed in, so they aren't swallowed during startup.
const newPaneHookDelay = 300 * time.Millisecond

// applyNewPaneHooks types the configured "hook new-pane" commands into a newly
// spawned pane. This is the .zshrc-style per-shell startup: e.g. activating a
// virtualenv or running a greeting. Commands are written after a short delay in
// a background goroutine so they don't race the shell's initialization.
func (ms *ManagedSession) applyNewPaneHooks(pane *session.Pane) {
	if pane == nil || len(ms.newPaneHooks) == 0 {
		return
	}
	hooks := ms.newPaneHooks
	go func() {
		time.Sleep(newPaneHookDelay)
		for _, h := range hooks {
			if pane.HasExited {
				return
			}
			_, _ = pane.Pty.WriteString(h + "\r")
		}
	}()
}

// applyRunCmds executes "run" startup commands against a freshly created
// session. Only a layout-building subset is supported here (splits, tabs,
// tab selection); richer dispatch belongs to interactive command mode.
func (ms *ManagedSession) applyRunCmds(cmds []string) {
	for _, c := range cmds {
		fields := strings.Fields(c)
		if len(fields) == 0 {
			continue
		}
		switch fields[0] {
		case "split-v", "split-vertical":
			ms.runSplit(session.SplitVertical)
		case "split-h", "split-horizontal":
			ms.runSplit(session.SplitHorizontal)
		case "new-tab", "new-window":
			ms.runNewTab()
		case "select-tab", "go-to-tab":
			if len(fields) > 1 {
				var idx int
				if _, err := fmt.Sscanf(fields[1], "%d", &idx); err == nil {
					ms.Session.GoToTab(idx - 1) // .saxrc tabs are 1-based
				}
			}
		}
	}
}

// runSplit splits the active pane and wires up the new pane's reader + hooks.
func (ms *ManagedSession) runSplit(dir session.SplitDir) {
	tab := ms.Session.CurrentTab()
	if tab == nil {
		return
	}
	cols, rows := ms.Session.PaneArea()
	w, h := cols, rows/2
	if dir == session.SplitVertical {
		w, h = cols/2, rows
	}
	pane, err := tab.SplitActive(dir, w, h)
	if err != nil {
		return
	}
	tab.ResizePanes(session.Rect{X: 0, Y: 0, W: cols, H: rows})
	ms.startPaneReader(pane)
	ms.applyNewPaneHooks(pane)
}

// runNewTab adds a tab and wires up its pane's reader + hooks.
func (ms *ManagedSession) runNewTab() {
	tab, err := ms.Session.AddTab()
	if err != nil {
		return
	}
	for _, pane := range tab.Panes {
		ms.startPaneReader(pane)
		ms.applyNewPaneHooks(pane)
	}
}
