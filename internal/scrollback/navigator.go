package scrollback

import (
	"fmt"
	"strings"
)

// Navigator provides vi-style navigation through scrollback history.
type Navigator struct {
	buf       *Buffer
	offset    int // scroll offset from bottom (0 = bottom)
	cursorX   int
	cursorY   int // relative to viewport
	markSet   bool
	markX     int
	markY     int // absolute line index in buffer
	viewW     int
	viewH     int
	search    string
	searching bool
	searchBuf string
}

// NewNavigator creates a navigator for the given buffer.
func NewNavigator(buf *Buffer, viewW, viewH int) *Navigator {
	return &Navigator{
		buf:   buf,
		viewW: viewW,
		viewH: viewH,
	}
}

// HandleKey processes a key action and returns (should_exit, yanked_text).
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
		n.moveLeft(1)
	case "l", "right":
		n.moveRight(1)
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
		line := n.currentLine()
		n.cursorX = len(line) - 1
		if n.cursorX < 0 {
			n.cursorX = 0
		}
	case "space":
		n.toggleMark()
	case "y":
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

func (n *Navigator) moveDown(count int) {
	if n.offset-count < 0 {
		n.offset = 0
	} else {
		n.offset -= count
	}
}

func (n *Navigator) moveUp(count int) {
	maxOffset := n.buf.LineCount() - n.viewH
	if maxOffset < 0 {
		maxOffset = 0
	}
	n.offset += count
	if n.offset > maxOffset {
		n.offset = maxOffset
	}
}

func (n *Navigator) moveLeft(count int) {
	n.cursorX -= count
	if n.cursorX < 0 {
		n.cursorX = 0
	}
}

func (n *Navigator) moveRight(count int) {
	n.cursorX += count
}

func (n *Navigator) goToTop() {
	maxOffset := n.buf.LineCount() - n.viewH
	if maxOffset < 0 {
		maxOffset = 0
	}
	n.offset = maxOffset
}

func (n *Navigator) goToBottom() {
	n.offset = 0
}

func (n *Navigator) currentLine() string {
	lines := n.visibleLines()
	absY := n.cursorY
	if absY >= 0 && absY < len(lines) {
		return lines[absY]
	}
	return ""
}

func (n *Navigator) toggleMark() {
	if n.markSet {
		n.markSet = false
	} else {
		n.markSet = true
		n.markX = n.cursorX
		// Calculate absolute line index
		n.markY = n.absoluteLineIndex(n.cursorY)
	}
}

func (n *Navigator) absoluteLineIndex(viewY int) int {
	total := n.buf.LineCount()
	startLine := total - n.viewH - n.offset
	if startLine < 0 {
		startLine = 0
	}
	return startLine + viewY
}

func (n *Navigator) yankSelection() string {
	if !n.markSet {
		return ""
	}

	curAbsY := n.absoluteLineIndex(n.cursorY)
	startY, endY := n.markY, curAbsY
	startX, endX := n.markX, n.cursorX

	if startY > endY || (startY == endY && startX > endX) {
		startY, endY = endY, startY
		startX, endX = endX, startX
	}

	var result strings.Builder
	for y := startY; y <= endY; y++ {
		line := n.buf.LineAt(y)
		if y == startY && y == endY {
			if startX < len(line) {
				end := endX + 1
				if end > len(line) {
					end = len(line)
				}
				result.WriteString(line[startX:end])
			}
		} else if y == startY {
			if startX < len(line) {
				result.WriteString(line[startX:])
			}
			result.WriteByte('\n')
		} else if y == endY {
			end := endX + 1
			if end > len(line) {
				end = len(line)
			}
			result.WriteString(line[:end])
		} else {
			result.WriteString(line)
			result.WriteByte('\n')
		}
	}

	n.markSet = false
	return result.String()
}

func (n *Navigator) searchNext() {
	if n.search == "" {
		return
	}
	total := n.buf.LineCount()
	startLine := n.absoluteLineIndex(n.cursorY)
	for i := 1; i <= total; i++ {
		idx := (startLine + i) % total
		line := n.buf.LineAt(idx)
		if strings.Contains(strings.ToLower(line), strings.ToLower(n.search)) {
			// Scroll to this line
			n.offset = total - idx - n.viewH/2
			if n.offset < 0 {
				n.offset = 0
			}
			return
		}
	}
}

func (n *Navigator) searchPrev() {
	if n.search == "" {
		return
	}
	total := n.buf.LineCount()
	startLine := n.absoluteLineIndex(n.cursorY)
	for i := 1; i <= total; i++ {
		idx := (startLine - i + total) % total
		line := n.buf.LineAt(idx)
		if strings.Contains(strings.ToLower(line), strings.ToLower(n.search)) {
			n.offset = total - idx - n.viewH/2
			if n.offset < 0 {
				n.offset = 0
			}
			return
		}
	}
}

func (n *Navigator) visibleLines() []string {
	total := n.buf.LineCount()
	if total == 0 {
		return nil
	}

	startLine := total - n.viewH - n.offset
	if startLine < 0 {
		startLine = 0
	}
	endLine := startLine + n.viewH
	if endLine > total {
		endLine = total
	}

	lines := make([]string, 0, endLine-startLine)
	for i := startLine; i < endLine; i++ {
		lines = append(lines, n.buf.LineAt(i))
	}
	return lines
}

// Render produces the copy mode overlay.
func (n *Navigator) Render(width, height int) string {
	lines := n.visibleLines()

	var result []string

	// Header
	header := " COPY MODE "
	if n.searching {
		header = fmt.Sprintf(" SEARCH: %s_ ", n.searchBuf)
	} else if n.markSet {
		header = " COPY MODE [selecting] "
	}
	headerPad := width - len(header)
	if headerPad < 0 {
		headerPad = 0
	}
	result = append(result, header+strings.Repeat("-", headerPad))

	// Content area (height - 2 for header + footer)
	contentH := height - 2
	for i := 0; i < contentH; i++ {
		if i < len(lines) {
			line := lines[i]
			if len(line) > width {
				line = line[:width]
			}
			if len(line) < width {
				line += strings.Repeat(" ", width-len(line))
			}

			// Highlight cursor position
			if i == n.cursorY {
				runes := []rune(line)
				if n.cursorX < len(runes) {
					// Invert the cursor character
					runes[n.cursorX] = invertRune(runes[n.cursorX])
				}
				line = string(runes)
			}

			result = append(result, line)
		} else {
			result = append(result, strings.Repeat(" ", width))
		}
	}

	// Footer
	pos := fmt.Sprintf(" line %d/%d ", n.buf.LineCount()-n.offset, n.buf.LineCount())
	footerPad := width - len(pos)
	if footerPad < 0 {
		footerPad = 0
	}
	result = append(result, strings.Repeat("-", footerPad)+pos)

	return strings.Join(result, "\n")
}

func invertRune(r rune) rune {
	// Simple visual indicator — wrap with reverse video ANSI
	// For simplicity, return a block character as cursor indicator
	if r == ' ' {
		return '\u2588' // Full block
	}
	return r
}
