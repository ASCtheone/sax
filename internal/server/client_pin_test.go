//go:build !windows

package server

import (
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

// attachTest connects a client to ms the way the server does, pinned to tab
// ("" = whole session). Whatever the server sends is discarded.
func attachTest(t *testing.T, s *Server, ms *ManagedSession, tab string) *ClientConn {
	t.Helper()
	local, remote := net.Pipe()
	go func() { _, _ = io.Copy(io.Discard, remote) }()
	t.Cleanup(func() { remote.Close() })

	cc := newClientConn(local, ms, s)
	ms.AddClient(cc)
	t.Cleanup(func() { ms.RemoveClient(cc.ID); cc.Close() })
	ms.WithLock(func() { cc.handleAttach(AttachMsg{W: 100, H: 30, Tab: tab}) })
	return cc
}

func frameOf(cc *ClientConn) string {
	var frame string
	cc.ms.WithLock(func() { frame, _, _ = cc.buildFrame() })
	return ansi.Strip(frame)
}

func closed(cc *ClientConn) bool {
	select {
	case <-cc.done:
		return true
	case <-time.After(time.Second):
		return false
	}
}

// sax -a v1.api: a client pinned to one app sees and types into only that
// app, cannot switch tabs, and leaves group clients (sax -a v1) alone.
func TestPinnedClient(t *testing.T) {
	s := NewServer("pin-test")
	s.InitTabs = []TabSpec{
		{Name: "api", Cmd: []string{"sh"}, Input: "echo from-api"},
		{Name: "web", Cmd: []string{"sh"}, Input: "echo from-web"},
	}
	ms, err := s.ensureSession(defaultCols, defaultRows)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Shutdown)
	waitRender(t, ms.paneFor("api"), "from-api")
	waitRender(t, ms.paneFor("web"), "from-web")

	group := attachTest(t, s, ms, "")
	web := attachTest(t, s, ms, "web")

	frame := frameOf(web)
	if !strings.Contains(frame, "from-web") || strings.Contains(frame, "from-api") {
		t.Errorf("pinned client should see only web:\n%s", frame)
	}
	if bar := strings.SplitN(frame, "\n", 2)[0]; strings.Contains(bar, "api") || !strings.Contains(bar, "web") {
		t.Errorf("pinned tab bar = %q, want only web", bar)
	}
	if frame := frameOf(group); !strings.Contains(frame, "from-api") {
		t.Errorf("group client should still see the active tab (api):\n%s", frame)
	}

	ms.WithLock(func() { web.handleKeyInput(KeyInputMsg{Data: []byte("echo typed-into-web\r")}) })
	waitRender(t, ms.paneFor("web"), "typed-into-web")
	if strings.Contains(ms.paneFor("api").Render(), "typed-into-web") {
		t.Error("keys of the pinned client reached api")
	}

	tabs := len(ms.Session.Tabs)
	for _, cmd := range []CommandMsg{{Cmd: CmdNextTab}, {Cmd: CmdGoToTab, Args: "1"}, {Cmd: CmdCreateTab}, {Cmd: CmdCloseTab}, {Cmd: CmdWindowList}} {
		ms.WithLock(func() { web.handleCommand(cmd) })
	}
	if ms.Session.ActiveTab != 0 || len(ms.Session.Tabs) != tabs || web.windowList {
		t.Errorf("tab commands must be ignored when pinned: active=%d tabs=%d windowList=%v",
			ms.Session.ActiveTab, len(ms.Session.Tabs), web.windowList)
	}
	ms.WithLock(func() { group.handleCommand(CommandMsg{Cmd: CmdNextTab}) })
	if ms.Session.ActiveTab != 1 {
		t.Error("group clients still switch tabs")
	}

	// The app's tab closing (it exited, or net down removed it) detaches
	// the pinned client only.
	ms.WithLock(func() { ms.Session.RemoveTab(tabIndex(ms.Session, ms.Session.TabByName("web"))) })
	web.sendFrame()
	if !closed(web) {
		t.Error("pinned client should be detached once its tab is gone")
	}
	group.sendFrame()
	select {
	case <-group.done:
		t.Error("group client must stay attached")
	default:
	}
}

func TestPinnedClient_UnknownTab(t *testing.T) {
	s := NewServer("pin-test-2")
	s.InitTabs = []TabSpec{{Name: "api", Cmd: []string{"sh"}}}
	ms, err := s.ensureSession(defaultCols, defaultRows)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Shutdown)

	if cc := attachTest(t, s, ms, "ghost"); !closed(cc) {
		t.Error("attaching to a missing tab should be refused")
	}
}
