package server

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestOverlayCursorWrapsCellInReverseVideo(t *testing.T) {
	got := overlayCursor("hello\nworld", 1, 1)
	want := "hello\nw" + cursorOn + "o" + cursorOff + "rld"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestOverlayCursorPastEndOfLinePads(t *testing.T) {
	got := overlayCursor("ab", 4, 0)
	want := "ab  " + cursorOn + " " + cursorOff
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestOverlayCursorKeepsStyledContentWidth(t *testing.T) {
	line := "\x1b[31mred\x1b[0m text"
	got := overlayCursor(line, 2, 0)
	if ansi.StringWidth(got) != ansi.StringWidth(line) {
		t.Fatalf("width changed: %d -> %d (%q)", ansi.StringWidth(line), ansi.StringWidth(got), got)
	}
	if ansi.Strip(got) != "red text" {
		t.Fatalf("text changed: %q", ansi.Strip(got))
	}
}

func TestOverlayCursorOutOfRangeRowIsNoop(t *testing.T) {
	in := "one\ntwo"
	if got := overlayCursor(in, 0, 5); got != in {
		t.Fatalf("got %q, want unchanged", got)
	}
	if got := overlayCursor(in, -1, 0); got != in {
		t.Fatalf("got %q, want unchanged", got)
	}
}

// ansi.Cut prefixes the cut cell with the accumulated SGR state, which can
// include a reset. Nothing may sit between cursorOn and the cell's text, or
// the reset cancels the reverse video and the cursor is invisible.
func TestOverlayCursorNotCancelledByStyleReset(t *testing.T) {
	line := "\x1b[32;1mab\x1b[mcd"
	got := overlayCursor(line, 2, 0)
	if !strings.Contains(got, cursorOn+"c"+cursorOff) {
		t.Fatalf("cursor cell not directly reversed: %q", got)
	}
	if ansi.Strip(got) != "abcd" {
		t.Fatalf("text changed: %q", ansi.Strip(got))
	}
}
