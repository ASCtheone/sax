//go:build !windows

package pty

import (
	"strings"
	"testing"
	"time"
)

// When the command exits, reading the PTY must end so the server notices
// (on Linux this only happens once the parent's copy of the slave is closed).
func TestReadEndsWhenCommandExits(t *testing.T) {
	p, err := StartCommand(80, 24, "sh", []string{"-c", "echo pty-exit-ok"}, "")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	done := make(chan string, 1)
	go func() {
		var out strings.Builder
		buf := make([]byte, 1024)
		for {
			n, err := p.Read(buf)
			out.Write(buf[:n])
			if err != nil {
				done <- out.String()
				return
			}
		}
	}()
	select {
	case out := <-done:
		if !strings.Contains(out, "pty-exit-ok") {
			t.Errorf("output = %q", out)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("PTY read did not end after the command exited")
	}
}
