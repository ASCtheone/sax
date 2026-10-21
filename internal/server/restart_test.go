//go:build !windows

package server

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func readPid(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil {
				return pid
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("no pid in %s", path)
	return 0
}

// sax net restart: the only tab of a session is replaced without ending the
// session, the old tab's whole process tree is stopped first, and clients
// pinned to the old tab follow the new one.
func TestReplaceTab(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	s := NewServer("replace-test")
	s.InitTabs = []TabSpec{{
		Name: "api", Cmd: []string{"sh"},
		Input: "sleep 300 & echo $! > " + pidFile + "; echo run-1",
	}}
	ms, err := s.ensureSession(defaultCols, defaultRows)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Shutdown)
	waitRender(t, ms.paneFor("api"), "run-1")
	child := readPid(t, pidFile)
	oldTab := ms.Session.Tabs[0]
	pinned := attachTest(t, s, ms, "api")

	killed, err := ms.replaceTab(TabSpec{Name: "api", Cmd: []string{"sh"}, Input: "echo run-2"})
	if err != nil {
		t.Fatal(err)
	}
	if killed < 2 {
		t.Errorf("killed %d processes, want the shell and its background child", killed)
	}
	if p, ok := processTable()[child]; ok && !p.zombie {
		t.Errorf("background child %d survived the restart", child)
	}

	waitRender(t, ms.paneFor("api"), "run-2")
	select {
	case <-s.done:
		t.Fatal("replacing the only tab ended the session")
	default:
	}
	var tabs int
	var same bool
	ms.WithLock(func() { tabs, same = len(ms.Session.Tabs), ms.Session.Tabs[0] == oldTab })
	if tabs != 1 || same {
		t.Errorf("tabs = %d, old tab still in place = %v", tabs, same)
	}
	if frame := frameOf(pinned); !strings.Contains(frame, "run-2") {
		t.Errorf("pinned client should follow the new tab:\n%s", frame)
	}
	time.Sleep(300 * time.Millisecond) // the old shell's exit must stay a no-op
	select {
	case <-s.done:
		t.Fatal("the old shell exiting ended the session")
	default:
	}

	if _, err := ms.replaceTab(TabSpec{Name: "ghost", Cmd: []string{"sh"}}); err == nil {
		t.Error("replacing a missing tab should fail")
	}
}
