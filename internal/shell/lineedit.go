package shell

import (
	"fmt"
	"io"
	"strings"

	"github.com/asc/sax/internal/shell/history"
)

// edAction is the outcome of feeding one byte to the editor.
type edAction int

const (
	edNone   edAction = iota // byte consumed, keep editing
	edSubmit                 // line complete (Enter)
	edCancel                 // Ctrl+C: discard the line
	edEOF                    // Ctrl+D on an empty line
)

// Control bytes the editor recognizes.
const (
	keyCtrlA = 0x01
	keyCtrlC = 0x03
	keyCtrlD = 0x04
	keyCtrlE = 0x05
	keyCtrlH = 0x08
	keyCtrlR = 0x12
	keyCtrlU = 0x15
	keyEsc   = 0x1b
	keyCR    = '\r'
	keyLF    = '\n'
	keyDEL   = 0x7f
)

// escState tracks parsing of ANSI escape sequences (arrow keys, etc.).
type escState int

const (
	escNone escState = iota
	escStart
	escCSI
)

// Editor is a cursor-aware, single-line input reader with history navigation
// and reverse-incremental search. It owns rendering of the current line, so it
// must run with the terminal in raw mode.
type Editor struct {
	out  io.Writer
	hist *history.History

	prompt string
	buf    []rune
	cursor int

	// History browsing: browse == -1 means editing a fresh line; otherwise it
	// is an index into history, with stash holding the fresh line. prefix is the
	// text typed when browsing began; ↑/↓ only visit entries starting with it
	// (like zsh/fish), so an empty prefix cycles everything.
	browse int
	stash  []rune
	prefix string

	// Escape-sequence parsing.
	esc    escState
	escBuf []byte

	// Reverse search.
	searching bool
	query     []rune
	matchIdx  int
}

func newEditor(out io.Writer, hist *history.History) *Editor {
	return &Editor{out: out, hist: hist, browse: -1, matchIdx: -1}
}

// start begins a fresh line with the given prompt and draws it.
func (e *Editor) start(prompt string) {
	e.prompt = prompt
	e.buf = e.buf[:0]
	e.cursor = 0
	e.browse = -1
	e.stash = nil
	e.prefix = ""
	e.esc = escNone
	e.escBuf = e.escBuf[:0]
	e.searching = false
	e.query = e.query[:0]
	e.matchIdx = -1
	io.WriteString(e.out, prompt)
}

// feed processes one input byte and returns an action. For edSubmit the second
// value is the completed line.
func (e *Editor) feed(b byte) (edAction, string) {
	if e.esc != escNone {
		return e.feedEsc(b)
	}
	if b == keyEsc {
		e.esc = escStart
		e.escBuf = e.escBuf[:0]
		return edNone, ""
	}
	if e.searching {
		return e.feedSearch(b)
	}

	switch b {
	case keyCR, keyLF:
		return edSubmit, string(e.buf)
	case keyCtrlC:
		return edCancel, ""
	case keyCtrlD:
		if len(e.buf) == 0 {
			return edEOF, ""
		}
		e.deleteAtCursor()
	case keyCtrlR:
		e.startSearch()
	case keyCtrlA:
		e.cursor = 0
		e.redraw()
	case keyCtrlE:
		e.cursor = len(e.buf)
		e.redraw()
	case keyCtrlU:
		e.browse = -1
		e.buf = e.buf[:0]
		e.cursor = 0
		e.redraw()
	case keyDEL, keyCtrlH:
		e.backspace()
	default:
		if b >= 0x20 {
			e.insert(rune(b))
		}
	}
	return edNone, ""
}

// --- line editing ---------------------------------------------------------

func (e *Editor) insert(r rune) {
	e.browse = -1 // editing starts a fresh line for the next ↑
	e.buf = append(e.buf, 0)
	copy(e.buf[e.cursor+1:], e.buf[e.cursor:])
	e.buf[e.cursor] = r
	e.cursor++
	e.redraw()
}

func (e *Editor) backspace() {
	if e.cursor == 0 {
		return
	}
	e.browse = -1
	e.buf = append(e.buf[:e.cursor-1], e.buf[e.cursor:]...)
	e.cursor--
	e.redraw()
}

func (e *Editor) deleteAtCursor() {
	if e.cursor >= len(e.buf) {
		return
	}
	e.browse = -1
	e.buf = append(e.buf[:e.cursor], e.buf[e.cursor+1:]...)
	e.redraw()
}

func (e *Editor) moveLeft() {
	if e.cursor > 0 {
		e.cursor--
		e.redraw()
	}
}

func (e *Editor) moveRight() {
	if e.cursor < len(e.buf) {
		e.cursor++
		e.redraw()
	}
}

func (e *Editor) setLine(s string) {
	e.buf = []rune(s)
	e.cursor = len(e.buf)
	e.redraw()
}

// redraw rewrites the whole line: CR to column 0, prompt, buffer, clear to end,
// then reposition the cursor.
func (e *Editor) redraw() {
	if e.searching {
		e.redrawSearch()
		return
	}
	var b strings.Builder
	b.WriteByte('\r')
	b.WriteString(e.prompt)
	b.WriteString(string(e.buf))
	b.WriteString("\x1b[K")
	if tail := len(e.buf) - e.cursor; tail > 0 {
		fmt.Fprintf(&b, "\x1b[%dD", tail)
	}
	io.WriteString(e.out, b.String())
}

// --- history navigation ---------------------------------------------------

// historyPrev recalls the previous (older) history entry that starts with the
// text typed before browsing began. With nothing typed, the prefix is empty and
// every entry matches, so it cycles all of history.
func (e *Editor) historyPrev() {
	if e.hist == nil || e.hist.Len() == 0 {
		return
	}
	prefix := e.prefix
	from := e.browse - 1
	if e.browse == -1 {
		prefix = string(e.buf) // whole line at the moment ↑ is first pressed
		from = e.hist.Len() - 1
	}

	idx := e.searchPrefix(prefix, from, -1)
	if idx < 0 {
		return // no older match; leave the line untouched
	}
	if e.browse == -1 {
		e.stash = append([]rune(nil), e.buf...)
		e.prefix = prefix
	}
	e.browse = idx
	e.setLine(e.hist.At(idx))
}

// historyNext moves toward newer matching entries, restoring the in-progress
// line once it goes past the newest match.
func (e *Editor) historyNext() {
	if e.browse == -1 {
		return
	}
	idx := e.searchPrefix(e.prefix, e.browse+1, +1)
	if idx < 0 {
		e.browse = -1
		e.setLine(string(e.stash))
		return
	}
	e.browse = idx
	e.setLine(e.hist.At(idx))
}

// searchPrefix scans history from index `from` in the given direction (-1 older,
// +1 newer) for the first entry with the given prefix, returning its index or -1.
func (e *Editor) searchPrefix(prefix string, from, dir int) int {
	for i := from; i >= 0 && i < e.hist.Len(); i += dir {
		if strings.HasPrefix(e.hist.At(i), prefix) {
			return i
		}
	}
	return -1
}

// --- escape sequences -----------------------------------------------------

func (e *Editor) feedEsc(b byte) (edAction, string) {
	switch e.esc {
	case escStart:
		if b == '[' || b == 'O' {
			e.esc = escCSI
			return edNone, ""
		}
		// A lone ESC, then another key: cancel any search and reprocess.
		e.esc = escNone
		if e.searching {
			e.cancelSearch()
		}
		return e.feed(b)
	case escCSI:
		e.escBuf = append(e.escBuf, b)
		if (b >= 'A' && b <= 'Z') || b == '~' {
			seq := string(e.escBuf)
			e.esc = escNone
			e.escBuf = e.escBuf[:0]
			if !e.searching {
				e.handleCSI(seq)
			}
		}
	}
	return edNone, ""
}

func (e *Editor) handleCSI(seq string) {
	switch seq {
	case "A":
		e.historyPrev()
	case "B":
		e.historyNext()
	case "C":
		e.moveRight()
	case "D":
		e.moveLeft()
	case "H", "1~", "7~":
		e.cursor = 0
		e.redraw()
	case "F", "4~", "8~":
		e.cursor = len(e.buf)
		e.redraw()
	case "3~":
		e.deleteAtCursor()
	}
}

// --- reverse search -------------------------------------------------------

func (e *Editor) startSearch() {
	if e.hist == nil || e.hist.Len() == 0 {
		return
	}
	e.searching = true
	e.query = e.query[:0]
	e.matchIdx = e.hist.Len() - 1
	e.redrawSearch()
}

func (e *Editor) feedSearch(b byte) (edAction, string) {
	switch b {
	case keyCR, keyLF:
		line := e.acceptSearch()
		e.redraw()
		return edSubmit, line
	case keyCtrlC:
		e.cancelSearch()
		return edNone, ""
	case keyCtrlR:
		if e.matchIdx > 0 {
			if idx := e.hist.SearchBackward(string(e.query), e.matchIdx-1); idx >= 0 {
				e.matchIdx = idx
			}
		}
		e.redrawSearch()
	case keyDEL, keyCtrlH:
		if len(e.query) > 0 {
			e.query = e.query[:len(e.query)-1]
		}
		e.runSearch()
	default:
		if b < 0x20 {
			// Any other control key accepts the match, then is applied.
			e.acceptSearch()
			e.redraw()
			return e.feed(b)
		}
		e.query = append(e.query, rune(b))
		e.runSearch()
	}
	return edNone, ""
}

func (e *Editor) runSearch() {
	e.matchIdx = e.hist.SearchBackward(string(e.query), e.hist.Len()-1)
	e.redrawSearch()
}

func (e *Editor) acceptSearch() string {
	e.searching = false
	if e.matchIdx >= 0 {
		e.buf = []rune(e.hist.At(e.matchIdx))
	}
	e.cursor = len(e.buf)
	return string(e.buf)
}

func (e *Editor) cancelSearch() {
	e.searching = false
	e.query = e.query[:0]
	e.redraw()
}

func (e *Editor) redrawSearch() {
	match := ""
	if e.matchIdx >= 0 {
		match = e.hist.At(e.matchIdx)
	}
	var b strings.Builder
	b.WriteByte('\r')
	fmt.Fprintf(&b, "(reverse-i-search)`%s': %s", string(e.query), match)
	b.WriteString("\x1b[K")
	io.WriteString(e.out, b.String())
}

// --- crlfWriter -----------------------------------------------------------

// crlfWriter translates lone '\n' into '\r\n'. In raw mode the terminal does no
// newline translation, so shell-generated text is routed through this to avoid
// the staircase effect. Child program output already carries CRLF and bypasses
// this.
type crlfWriter struct {
	w io.Writer
}

func (c crlfWriter) Write(p []byte) (int, error) {
	out := make([]byte, 0, len(p)+8)
	var prev byte
	for _, b := range p {
		if b == '\n' && prev != '\r' {
			out = append(out, '\r', '\n')
		} else {
			out = append(out, b)
		}
		prev = b
	}
	if _, err := c.w.Write(out); err != nil {
		return 0, err
	}
	return len(p), nil
}
