package terminal

import "testing"

func TestCursorTracksPositionAndVisibility(t *testing.T) {
	term := New(20, 5)

	x, y, visible := term.Cursor()
	if x != 0 || y != 0 || !visible {
		t.Fatalf("initial cursor = (%d,%d,%v), want (0,0,true)", x, y, visible)
	}

	_, _ = term.Write([]byte("abc\r\nde"))
	x, y, visible = term.Cursor()
	if x != 2 || y != 1 || !visible {
		t.Fatalf("after write cursor = (%d,%d,%v), want (2,1,true)", x, y, visible)
	}

	_, _ = term.Write([]byte("\x1b[?25l"))
	if _, _, visible = term.Cursor(); visible {
		t.Fatal("cursor should be hidden after DECTCEM reset")
	}

	_, _ = term.Write([]byte("\x1b[?25h"))
	if _, _, visible = term.Cursor(); !visible {
		t.Fatal("cursor should be visible after DECTCEM set")
	}
}
