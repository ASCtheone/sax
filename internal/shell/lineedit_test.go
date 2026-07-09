package shell

import (
	"io"
	"strings"
	"testing"

	"github.com/asc/sax/internal/shell/history"
)

// feedString feeds each byte to the editor, returning the first non-None action
// and its line text.
func feedString(e *Editor, s string) (edAction, string) {
	for i := 0; i < len(s); i++ {
		if act, line := e.feed(s[i]); act != edNone {
			return act, line
		}
	}
	return edNone, ""
}

func newTestEditor(h *history.History) *Editor {
	e := newEditor(io.Discard, h)
	e.start("> ")
	return e
}

func TestEditorSubmit(t *testing.T) {
	e := newTestEditor(history.New("", 0))
	act, line := feedString(e, "echo hi\r")
	if act != edSubmit || line != "echo hi" {
		t.Fatalf("got (%v, %q), want (submit, %q)", act, line, "echo hi")
	}
}

func TestEditorInsertAtCursor(t *testing.T) {
	e := newTestEditor(history.New("", 0))
	// type "ac", move left (ESC [ D), insert "b" -> "abc"
	act, line := feedString(e, "ac\x1b[Db\r")
	if act != edSubmit || line != "abc" {
		t.Fatalf("cursor insert = (%v, %q), want (submit, abc)", act, line)
	}
}

func TestEditorBackspace(t *testing.T) {
	e := newTestEditor(history.New("", 0))
	act, line := feedString(e, "abx\x7fc\r") // x deleted, then c
	if act != edSubmit || line != "abc" {
		t.Fatalf("backspace = (%v, %q), want (submit, abc)", act, line)
	}
}

func TestEditorDeleteKey(t *testing.T) {
	e := newTestEditor(history.New("", 0))
	// "abc", Home, Delete (removes 'a'), submit -> "bc"
	act, line := feedString(e, "abc\x1b[H\x1b[3~\r")
	if act != edSubmit || line != "bc" {
		t.Fatalf("delete key = (%v, %q), want (submit, bc)", act, line)
	}
}

func TestEditorCtrlC(t *testing.T) {
	e := newTestEditor(history.New("", 0))
	if act, _ := feedString(e, "junk\x03"); act != edCancel {
		t.Fatalf("Ctrl+C should cancel, got %v", act)
	}
}

func TestEditorCtrlDEmptyVsNonEmpty(t *testing.T) {
	e := newTestEditor(history.New("", 0))
	if act, _ := e.feed(keyCtrlD); act != edEOF {
		t.Fatalf("Ctrl+D on empty = %v, want eof", act)
	}
	e.start("> ")
	feedString(e, "abc")
	if act, _ := e.feed(keyCtrlD); act != edNone {
		t.Fatalf("Ctrl+D on non-empty (cursor at end) = %v, want none", act)
	}
}

func TestEditorHistoryNavigation(t *testing.T) {
	h := history.New("", 0)
	h.Add("one")
	h.Add("two")
	e := newTestEditor(h)
	// Up -> "two", Up -> "one", Down -> "two", submit.
	act, line := feedString(e, "\x1b[A\x1b[A\x1b[B\r")
	if act != edSubmit || line != "two" {
		t.Fatalf("history nav = (%v, %q), want (submit, two)", act, line)
	}
}

func TestEditorHistoryPrefixSearch(t *testing.T) {
	h := history.New("", 0)
	h.Add("git status")
	h.Add("ls -la")
	h.Add("git commit")
	e := newTestEditor(h)
	// Type "git", then ↑ recalls newest "git " match ("git commit"); ↑ again
	// skips "ls -la" and lands on "git status".
	act, line := feedString(e, "git\x1b[A\x1b[A\r")
	if act != edSubmit || line != "git status" {
		t.Fatalf("prefix ↑ = (%v, %q), want (submit, git status)", act, line)
	}
}

func TestEditorHistoryPrefixThenDown(t *testing.T) {
	h := history.New("", 0)
	h.Add("git status")
	h.Add("ls -la")
	h.Add("git commit")
	e := newTestEditor(h)
	// "git" + ↑↑ -> "git status"; ↓ -> back to "git commit" (newer match).
	act, line := feedString(e, "git\x1b[A\x1b[A\x1b[B\r")
	if act != edSubmit || line != "git commit" {
		t.Fatalf("prefix ↑↑↓ = (%v, %q), want (submit, git commit)", act, line)
	}
}

func TestEditorHistoryDownRestoresFreshLine(t *testing.T) {
	h := history.New("", 0)
	h.Add("old")
	e := newTestEditor(h)
	// type "fresh", Up (-> "old"), Down (-> back to "fresh"), submit.
	act, line := feedString(e, "fresh\x1b[A\x1b[B\r")
	if act != edSubmit || line != "fresh" {
		t.Fatalf("down restore = (%v, %q), want (submit, fresh)", act, line)
	}
}

func TestEditorReverseSearch(t *testing.T) {
	h := history.New("", 0)
	h.Add("git status")
	h.Add("ls -la")
	h.Add("git commit")
	e := newTestEditor(h)
	// Ctrl+R, type "stat" -> matches "git status", Enter accepts.
	act, line := feedString(e, "\x12stat\r")
	if act != edSubmit || line != "git status" {
		t.Fatalf("reverse search = (%v, %q), want (submit, git status)", act, line)
	}
}

func TestEditorReverseSearchCancel(t *testing.T) {
	h := history.New("", 0)
	h.Add("git status")
	e := newTestEditor(h)
	// type "keep", Ctrl+R, type "git", Ctrl+C cancels search (keeps line), submit.
	act, line := feedString(e, "keep\x12git\x03\r")
	if act != edSubmit || line != "keep" {
		t.Fatalf("search cancel = (%v, %q), want (submit, keep)", act, line)
	}
}

func TestCRLFWriter(t *testing.T) {
	tests := []struct{ in, want string }{
		{"hello", "hello"},
		{"a\nb", "a\r\nb"},
		{"already\r\nthere", "already\r\nthere"},
		{"trailing\n", "trailing\r\n"},
	}
	for _, tt := range tests {
		var sb strings.Builder
		w := crlfWriter{&sb}
		n, err := w.Write([]byte(tt.in))
		if err != nil {
			t.Fatalf("write %q: %v", tt.in, err)
		}
		if n != len(tt.in) {
			t.Errorf("Write(%q) n=%d, want %d", tt.in, n, len(tt.in))
		}
		if sb.String() != tt.want {
			t.Errorf("crlfWriter(%q) = %q, want %q", tt.in, sb.String(), tt.want)
		}
	}
}
