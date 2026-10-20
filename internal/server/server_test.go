//go:build !windows

package server

import (
	"strings"
	"testing"
	"time"
)

// waitShutdown waits for the server to shut itself down once its only pane
// exits — the same path as production, so cleanup never races the exit handler.
func waitShutdown(t *testing.T, s *Server) {
	t.Helper()
	select {
	case <-s.done:
	case <-time.After(5 * time.Second):
		t.Error("server did not shut down after its command exited")
	}
}

// A detached session must start its command right away, not on first attach:
// `sax -x`, `sax net up` and MCP sax_exec all start servers nobody attaches to.
func TestEnsureSession_StartsCommandWithoutAttach(t *testing.T) {
	s := NewServer("eager-test")
	s.InitCmd = "sh"
	s.InitCmdArgs = []string{"-c", "echo eager-start-ok; sleep 1"}

	ms, err := s.ensureSession(defaultCols, defaultRows)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { waitShutdown(t, s) })

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(ms.Session.ActivePane().Render(), "eager-start-ok") {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("command output never appeared without an attached client:\n%s", ms.Session.ActivePane().Render())
}

func TestEnsureSession_ReturnsExisting(t *testing.T) {
	s := NewServer("eager-test-2")
	s.InitCmd = "sh"
	s.InitCmdArgs = []string{"-c", "sleep 1"}
	a, err := s.ensureSession(defaultCols, defaultRows)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { waitShutdown(t, s) })
	b, err := s.ensureSession(10, 10)
	if err != nil || a != b {
		t.Errorf("second ensureSession = %p, %v; want the same session %p", b, err, a)
	}
}

func TestTailLines_SkipsTrailingBlankRows(t *testing.T) {
	rendered := "first\nsecond\n   \n\n  \n"
	if got := tailLines(rendered, 2); got != "first\nsecond" {
		t.Errorf("tailLines = %q, want the last 2 non-blank-trailing lines", got)
	}
	if got := tailLines(rendered, 10); got != "first\nsecond" {
		t.Errorf("tailLines(10) = %q", got)
	}
	if got := tailLines("a\n\nb\n\n", 2); got != "\nb" {
		t.Errorf("blank lines inside the output must be kept: %q", got)
	}
	if got := tailLines("   \n\n", 3); got != "" {
		t.Errorf("all blank = %q", got)
	}
}

// A view session (sax net up) starts with one named tab per command.
func TestEnsureSession_InitTabs(t *testing.T) {
	s := NewServer("tabs-test")
	s.InitTabs = []TabSpec{
		{Name: "api", Cmd: []string{"sh", "-c", "echo from-api; sleep 30"}},
		{Name: "web", Cmd: []string{"sh", "-c", "echo from-web; sleep 30"}},
	}
	ms, err := s.ensureSession(defaultCols, defaultRows)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Shutdown) // closes every tab the way the daemon does

	if got := strings.Join(ms.Session.TabNames(), ","); got != "1:api,2:web" {
		t.Fatalf("tabs = %s", got)
	}
	if ms.Session.ActiveTab != 0 {
		t.Errorf("the first tab should be active, got %d", ms.Session.ActiveTab)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		web := ms.Session.Tabs[1].ActivePaneObj()
		if web != nil && strings.Contains(web.Render(), "from-web") {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Error("second tab's command never ran")
}
