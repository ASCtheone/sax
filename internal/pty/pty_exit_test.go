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

// A wrapper (the vnet supervisor on Linux) is prepended to every command.
func TestWrapperPrefixesCommands(t *testing.T) {
	SetWrapper([]string{"sh", "-c", `echo wrapped-by-sup; exec "$@"`, "sup"})
	defer SetWrapper(nil)

	p, err := StartCommand(80, 24, "echo", []string{"inner-command"}, "")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	var out strings.Builder
	buf := make([]byte, 1024)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		n, err := p.Read(buf)
		out.Write(buf[:n])
		if err != nil {
			break
		}
	}
	got := out.String()
	if !strings.Contains(got, "wrapped-by-sup") || !strings.Contains(got, "inner-command") {
		t.Errorf("output = %q, want the wrapper to run the command", got)
	}
}
