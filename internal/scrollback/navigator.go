package scrollback

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// Navigator provides vi-style navigation and selection through the
// scrollback buffer.
//
// The cursor is anchored to a monotonic line serial — not a viewport
// row — so it stays on the same logical line as the view scrolls or as
// new output arrives. The mark (selection start) is also a serial, so
// selections remain pinned to real lines even while the buffer grows.
type Navigator struct {
	buf *Buffer

	// Cursor position: a line serial + visible column.
	cursorSerial int64
	cursorX      int

	// Mark (selection anchor): a line serial + visible column.
	markSet    bool
	markSerial int64
	markX      int

	// Scroll offset from the buffer's current bottom, in lines.
	// 0 means "cursor view is anchored to the newest line".
	offset int

	// Viewport dimensions (content area, excluding header/footer).
	viewW int
	viewH int

	// Search state.
	search    string
	searching bool
	searchBuf string
}

// NewNavigator creates a navigator for the given buffer, with a content
// area of (viewW x viewH). The cursor starts on the most recent line.
func NewNavigator(buf *Buffer, viewW, viewH int) *Navigator {
	n := &Navigator{
		buf:   buf,
		viewW: viewW,
		viewH: viewH,
	}
	total := buf.TotalWritten()
	if total > 0 {
		n.cursorSerial = total - 1
	}
	return n
}

// HandleKey processes a key action and returns (shouldExit, yankedText).
// Yanked text is plain (SGR-stripped) so it is safe to place directly
// in the system clipboard.
func (n *Navigator) HandleKey(action string) (exit bool, yanked string) {
	if n.searching {
		return n.handleSearchKey(action)
	}

	switch action {
	case "j", "down":
		n.moveDown(1)
	case "k", "up":
		n.moveUp(1)
	case "h", "left":
		if n.cursorX > 0 {
			n.cursorX--
		}
	case "l", "right":
		line := n.lineAt(n.cursorSerial)
		w := ansi.StringWidth(line)
		if n.cursorX < w-1 {
			n.cursorX++
		}
	case "ctrl+f", "pagedown":
		n.moveDown(n.viewH - 1)
	case "ctrl+b", "pageup":
		n.moveUp(n.viewH - 1)
	case "ctrl+d":
		n.moveDown(n.viewH / 2)
	case "ctrl+u":
		n.moveUp(n.viewH / 2)
	case "g":
		n.goToTop()
	case "G":
		n.goToBottom()
	case "0":
		n.cursorX = 0
	case "$":
		line := n.lineAt(n.cursorSerial)
		n.cursorX = ansi.StringWidth(line) - 1
		if n.cursorX < 0 {
			n.cursorX = 0
		}
	case "space", "v":
		n.toggleMark()
	case "y", "enter":
		text := n.yankSelection()
		if text != "" {
			return true, text
		}
	case "/":
		n.searching = true
		n.searchBuf = ""
	case "n":
		n.searchNext()
	case "N":
		n.searchPrev()
	case "q", "escape":
		return true, ""
	}

	n.clampCursor()
	n.scrollToShowCursor()
	return false, ""
}

func (n *Navigator) handleSearchKey(action string) (exit bool, yanked string) {
	switch action {
	case "enter":
		n.search = n.searchBuf
		n.searching = false
		n.searchNext()
	case "escape":
		n.searching = false
		n.searchBuf = ""
	case "backspace":
		if len(n.searchBuf) > 0 {
			n.searchBuf = n.searchBuf[:len(n.searchBuf)-1]
		}
	default:
		if len(action) == 1 {
			n.searchBuf += action
		}
	}
	return false, ""
}

// Resize updates the viewport dimensions.
func (n *Navigator) Resize(viewW, viewH int) {
	n.viewW = viewW
	n.viewH = viewH
	n.scrollToShowCursor()
}

// --- Movement ------------------------------------------------------------

func (n *Navigator) moveDown(count int) {
	n.cursorSerial += int64(count)
}

func (n *Navigator) moveUp(count int) {
	n.cursorSerial -= int64(count)
}

func (n *Navigator) goToTop() {
	n.cursorSerial = n.buf.OldestSerial()
	n.cursorX = 0
}

func (n *Navigator) goToBottom() {
	total := n.buf.TotalWritten()
	if total > 0 {
		n.cursorSerial = total - 1
	}
	n.cursorX = 0
}

// clampCursor keeps cursorSerial within the buffer's retained range.
func (n *Navigator) clampCursor() {
	oldest := n.buf.OldestSerial()
	total := n.buf.TotalWritten()
	if total == 0 {
		n.cursorSerial = 0
		return
	}
	if n.cursorSerial < oldest {
		n.cursorSerial = oldest
	}
	if n.cursorSerial >= total {
		n.cursorSerial = total - 1
	}
	if n.cursorX < 0 {
		n.cursorX = 0
	}
}

// scrollToShowCursor adjusts offset so the cursor's line is visible.
func (n *Navigator) scrollToShowCursor() {
	total := n.buf.TotalWritten()
	if total == 0 || n.viewH <= 0 {
		return
	}
	// Current visible range: [topSerial, topSerial+viewH)
	topSerial := total - int64(n.viewH) - int64(n.offset)
	botSerial := topSerial + int64(n.viewH) - 1

	if n.cursorSerial < topSerial {
		// Scroll up so cursor lands on top row.
		n.offset = int(total - int64(n.viewH) - n.cursorSerial)
	} else if n.cursorSerial > botSerial {
		// Scroll down so cursor lands on bottom row.
		n.offset = int(total-1) - int(n.cursorSerial)
		if n.offset < 0 {
			n.offset = 0
		}
	}
	if n.offset < 0 {
		n.offset = 0
	}
	maxOff := int(total - int64(n.buf.OldestSerial()+1))
	if maxOff < 0 {
		maxOff = 0
	}
	if n.offset > maxOff {
		n.offset = maxOff
	}
}

// --- Selection -----------------------------------------------------------

func (n *Navigator) toggleMark() {
	if n.markSet {
		n.markSet = false
		return
	}
	n.markSet = true
	n.markSerial = n.cursorSerial
	n.markX = n.cursorX
}

// selectionRange returns the ordered (start, end) of the current selection,
// or (_, _, false) if no selection is active.
func (n *Navigator) selectionRange() (startSerial int64, startX int, endSerial int64, endX int, ok bool) {
	if !n.markSet {
		return 0, 0, 0, 0, false
	}
	startSerial, startX = n.markSerial, n.markX
	endSerial, endX = n.cursorSerial, n.cursorX
	if startSerial > endSerial || (startSerial == endSerial && startX > endX) {
		startSerial, endSerial = endSerial, startSerial
		startX, endX = endX, startX
	}
	return startSerial, startX, endSerial, endX, true
}

func (n *Navigator) yankSelection() string {
	startSerial, startX, endSerial, endX, ok := n.selectionRange()
	if !ok {
		return ""
	}

	var b strings.Builder
	for s := startSerial; s <= endSerial; s++ {
		line, ok := n.buf.LineAtSerial(s)
		if !ok {
			continue
		}
		plain := ansi.Strip(line)
		runes := []rune(plain)
		lineLen := len(runes)

		lo, hi := 0, lineLen
		if s == startSerial {
			lo = startX
		}
		if s == endSerial {
			hi = endX + 1
		}
		if lo < 0 {
			lo = 0
		}
		if hi > lineLen {
			hi = lineLen
		}
		if lo < hi {
			b.WriteString(string(runes[lo:hi]))
		}
		if s < endSerial {
			b.WriteByte('\n')
		}
	}
	n.markSet = false
	return b.String()
}

// --- Search --------------------------------------------------------------

func (n *Navigator) searchNext() {
	if n.search == "" {
		return
	}
	needle := strings.ToLower(n.search)
	total := n.buf.TotalWritten()
	oldest := n.buf.OldestSerial()
	for s := n.cursorSerial + 1; s < total; s++ {
		line, ok := n.buf.LineAtSerial(s)
		if !ok {
			continue
		}
		if strings.Contains(strings.ToLower(ansi.Strip(line)), needle) {
			n.cursorSerial = s
			n.cursorX = 0
			return
		}
	}
	// Wrap from oldest to cursor.
	for s := oldest; s < n.cursorSerial; s++ {
		line, ok := n.buf.LineAtSerial(s)
		if !ok {
			continue
		}
		if strings.Contains(strings.ToLower(ansi.Strip(line)), needle) {
			n.cursorSerial = s
			n.cursorX = 0
			return
		}
	}
}

func (n *Navigator) searchPrev() {
	if n.search == "" {
		return
	}
	needle := strings.ToLower(n.search)
	oldest := n.buf.OldestSerial()
	total := n.buf.TotalWritten()
	for s := n.cursorSerial - 1; s >= oldest; s-- {
		line, ok := n.buf.LineAtSerial(s)
		if !ok {
			continue
		}
		if strings.Contains(strings.ToLower(ansi.Strip(line)), needle) {
			n.cursorSerial = s
			n.cursorX = 0
			return
		}
	}
	// Wrap from total back down to cursor.
	for s := total - 1; s > n.cursorSerial; s-- {
		line, ok := n.buf.LineAtSerial(s)
		if !ok {
			continue
		}
		if strings.Contains(strings.ToLower(ansi.Strip(line)), needle) {
			n.cursorSerial = s
			n.cursorX = 0
			return
		}
	}
}

// --- Rendering -----------------------------------------------------------

// lineAt fetches a line by serial, returning "" for out-of-range requests.
func (n *Navigator) lineAt(serial int64) string {
	line, _ := n.buf.LineAtSerial(serial)
	return line
}

// topSerial returns the serial of the line at viewport row 0.
// May be negative / below OldestSerial if the buffer has fewer lines
// than the viewport height; callers should treat such rows as blank.
func (n *Navigator) topSerial() int64 {
	total := n.buf.TotalWritten()
	return total - int64(n.viewH) - int64(n.offset)
}

// Render produces the copy mode overlay with selection highlighting.
func (n *Navigator) Render(width, height int) string {
	// Refresh viewport width/height in case the caller resized.
	n.viewW = width
	n.viewH = height - 2 // reserve header + footer
	if n.viewH < 1 {
		n.viewH = 1
	}
	n.clampCursor()
	n.scrollToShowCursor()

	startSerial, startX, endSerial, endX, hasSel := n.selectionRange()

	var out []string

	// Header.
	header := " COPY MODE "
	switch {
	case n.searching:
		header = fmt.Sprintf(" SEARCH: %s_ ", n.searchBuf)
	case n.markSet:
		header = " COPY MODE [selecting — press y to yank] "
	}
	out = append(out, padRight(header, width, '-'))

	// Content rows.
	top := n.topSerial()
	total := n.buf.TotalWritten()
	oldest := n.buf.OldestSerial()
	for row := 0; row < n.viewH; row++ {
		serial := top + int64(row)
		var line string
		if serial >= oldest && serial < total {
			line = n.lineAt(serial)
		}

		// Determine selection range within this line, if any.
		selLo, selHi := -1, -1
		if hasSel && serial >= startSerial && serial <= endSerial {
			plain := ansi.Strip(line)
			w := len([]rune(plain))
			selLo = 0
			selHi = w
			if serial == startSerial {
				selLo = startX
			}
			if serial == endSerial {
				selHi = endX + 1
			}
			if selLo < 0 {
				selLo = 0
			}
			if selHi > w {
				selHi = w
			}
		}

		rendered := renderLineWithSelection(line, width, selLo, selHi)

		// Cursor indicator: render cursor when selection is off.
		if !hasSel && serial == n.cursorSerial {
			rendered = renderLineWithCursor(line, width, n.cursorX)
		}

		out = append(out, rendered)
	}

	// Footer.
	curLineNum := int(n.cursorSerial - oldest + 1)
	totalLines := n.buf.LineCount()
	pos := fmt.Sprintf(" line %d/%d col %d ", curLineNum, totalLines, n.cursorX+1)
	out = append(out, padLeft(pos, width, '-'))

	return strings.Join(out, "\n")
}

// renderLineWithSelection renders a single line padded to width, with the
// visible columns [lo, hi) wrapped in reverse-video SGR. Selection bounds
// of (-1, -1) mean no selection on this row.
func renderLineWithSelection(line string, width, lo, hi int) string {
	padded := padLineToWidth(line, width)
	if lo < 0 || hi <= lo {
		return padded
	}
	if hi > width {
		hi = width
	}

	left := ansi.Cut(padded, 0, lo)
	middle := ansi.Cut(padded, lo, hi)
	right := ansi.Cut(padded, hi, width)

	return left + "\x1b[7m" + middle + "\x1b[27m" + right
}

// renderLineWithCursor renders a single line padded to width with a
// reverse-video cursor cell at visible column cx.
func renderLineWithCursor(line string, width, cx int) string {
	padded := padLineToWidth(line, width)
	if cx < 0 {
		cx = 0
	}
	if cx >= width {
		cx = width - 1
	}
	left := ansi.Cut(padded, 0, cx)
	cell := ansi.Cut(padded, cx, cx+1)
	right := ansi.Cut(padded, cx+1, width)
	if cell == "" {
		cell = " "
	}
	return left + "\x1b[7m" + cell + "\x1b[27m" + right
}

// padLineToWidth truncates or space-pads a (possibly SGR-styled) line to
// exactly `width` visible columns.
func padLineToWidth(line string, width int) string {
	w := ansi.StringWidth(line)
	if w > width {
		return ansi.Truncate(line, width, "")
	}
	if w < width {
		return line + strings.Repeat(" ", width-w)
	}
	return line
}

// padRight right-pads s with fill to reach width.
func padRight(s string, width int, fill byte) string {
	w := ansi.StringWidth(s)
	if w >= width {
		return ansi.Truncate(s, width, "")
	}
	return s + strings.Repeat(string(fill), width-w)
}

// padLeft left-pads s with fill to reach width.
func padLeft(s string, width int, fill byte) string {
	w := ansi.StringWidth(s)
	if w >= width {
		return ansi.Truncate(s, width, "")
	}
	return strings.Repeat(string(fill), width-w) + s
}
