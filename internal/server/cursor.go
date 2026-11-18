package server

import (
	"strings"

	"github.com/asc/sax/internal/session"
	"github.com/charmbracelet/x/ansi"
)

// The client is a bubbletea program, which hides the outer terminal's cursor,
// so sax paints the active pane's cursor itself as a reverse-video cell.
const (
	cursorOn  = "\x1b[7m"
	cursorOff = "\x1b[27m"
)

// renderPaneWithCursor renders a pane, painting its cursor when the app
// running in it has the cursor enabled.
func renderPaneWithCursor(p *session.Pane) string {
	content := p.Render()
	if p.HasExited {
		return content
	}
	x, y, visible := p.Term.Cursor()
	if !visible {
		return content
	}
	return overlayCursor(content, x, y)
}

// overlayCursor wraps the cell at column x of row y in reverse video. Rows
// shorter than x are space-padded so a cursor after the text still shows.
func overlayCursor(content string, x, y int) string {
	if x < 0 || y < 0 {
		return content
	}
	lines := strings.Split(content, "\n")
	if y >= len(lines) {
		return content
	}
	line := lines[y]
	if w := ansi.StringWidth(line); w <= x {
		line += strings.Repeat(" ", x-w+1)
	}
	w := ansi.StringWidth(line)
	// Strip the cell's SGR prefix: ansi.Cut carries the accumulated style,
	// often including a reset, which would cancel the reverse video. The
	// right-hand cut re-emits its own style, so nothing after is affected.
	cell := ansi.Strip(ansi.Cut(line, x, x+1))
	if cell == "" {
		cell = " "
	}
	out := make([]string, len(lines))
	copy(out, lines)
	out[y] = ansi.Truncate(line, x, "") + cursorOn + cell + cursorOff + ansi.Cut(line, x+1, w)
	return strings.Join(out, "\n")
}
