//go:build !windows

package server

import (
	"strings"
	"testing"
	"time"
)

// A detached session must start its command right away, not on first attach:
// `sax -x`, `sax net up` and MCP sax_exec all start servers nobody attaches to.
func TestEnsureSession_StartsCommandWithoutAttach(t *testing.T) {
	s := NewServer("eager-test")
	s.InitCmd = "sh"
	s.InitCmdArgs = []string{"-c", "echo eager-start-ok; sleep 5"}

	ms, err := s.ensureSession(defaultCols, defaultRows)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ms.Session.Close() })

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
	s.InitCmdArgs = []string{"-c", "sleep 5"}
	a, err := s.ensureSession(defaultCols, defaultRows)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Session.Close() })
	b, err := s.ensureSession(10, 10)
	if err != nil || a != b {
		t.Errorf("second ensureSession = %p, %v; want the same session %p", b, err, a)
	}
}
