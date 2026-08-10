package terminal

import (
	"testing"
	"time"
)

// A TUI (e.g. Claude Code) sends a Device Attributes query (ESC[c) at startup.
// The vt emulator replies via an internal unbuffered pipe, so Write blocks until
// something drains Read. If nothing does, the pane reader stalls and the pane
// renders blank. This guards that Read drains the response so Write completes.
func TestQueryResponseDoesNotBlockWrite(t *testing.T) {
	term := New(80, 24)

	got := make(chan int, 1)
	go func() {
		buf := make([]byte, 256)
		n, _ := term.Read(buf) // the DA response
		got <- n
	}()

	writeDone := make(chan struct{})
	go func() {
		_, _ = term.Write([]byte("\x1b[c")) // Device Attributes query
		close(writeDone)
	}()

	select {
	case <-writeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("Write blocked on query response — the blank-pane deadlock")
	}

	select {
	case n := <-got:
		if n == 0 {
			t.Fatal("no Device Attributes response emitted")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no response drained from Read")
	}
}
