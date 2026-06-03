package scrollback

import (
	"strings"
	"sync"
	"unicode/utf8"
)

const DefaultCapacity = 10000

// configuredCapacity is the line capacity used for new buffers. It defaults to
// DefaultCapacity and may be overridden once at daemon startup from ~/.saxrc
// ("set history-limit"). Treated as read-only after startup.
var configuredCapacity = DefaultCapacity

// SetDefaultCapacity overrides the capacity used by buffers created after this
// call. A non-positive value is ignored. Call once at startup.
func SetDefaultCapacity(n int) {
	if n > 0 {
		configuredCapacity = n
	}
}

// ConfiguredCapacity returns the currently configured default buffer capacity.
func ConfiguredCapacity() int {
	return configuredCapacity
}

// styledRune is a rune with the SGR prefix that should precede it on render.
// The sgr field is the full ANSI sequence needed to establish this rune's
// style from a default (reset) state — e.g. "\x1b[31m" or "\x1b[1;33m".
// An empty sgr means "default style".
type styledRune struct {
	r   rune
	sgr string
}

// Buffer is a ring buffer of terminal lines with style preservation.
//
// AppendOutput feeds raw PTY bytes through a small line-assembly state
// machine that understands \r, \b, \t, CSI erase-in-line sequences
// (\x1b[K, \x1b[0K, \x1b[1K, \x1b[2K), and SGR color/attribute sequences
// (\x1b[...m). Each rune is stored with the SGR state that was active
// when it was written, so scrollback retains the colors and attributes
// the user saw live — the scroll view and the live terminal match.
//
// The \r handling prevents progress bars and spinners from polluting the
// buffer with many near-identical entries: they rewrite the same pending
// line and only the final state is committed on \n.
//
// A monotonic serial number is assigned to each committed line. Consumers
// that want a stable view across concurrent output (e.g. mouse-wheel
// scrollback) can anchor on a serial via LineAtSerial.
type Buffer struct {
	mu sync.RWMutex

	lines    []string // serialized SGR+text lines in the ring
	head     int      // next write position
	count    int
	capacity int

	// totalWritten is the monotonic number of lines ever committed.
	// Oldest line still in the buffer has serial (totalWritten - count).
	totalWritten int64

	// Line assembly state — the partial line currently being built.
	// Committed to the ring (serialized) on \n.
	pending    []styledRune
	pendingCol int
	currentSGR string
}

// NewBuffer creates a new scrollback buffer with the given line capacity.
func NewBuffer(capacity int) *Buffer {
	if capacity <= 0 {
		capacity = DefaultCapacity
	}
	return &Buffer{
		lines:    make([]string, capacity),
		capacity: capacity,
	}
}

// AppendLine commits a fully-formed line to the ring buffer.
// Intended for tests and callers that already have an assembled line;
// normal PTY output should go through AppendOutput.
func (b *Buffer) AppendLine(line string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.commitLine(line)
}

// AppendOutput processes raw terminal output, assembling lines while
// honoring carriage-return overwrite semantics and tracking SGR state.
func (b *Buffer) AppendOutput(data []byte) {
	if len(data) == 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()

	i := 0
	for i < len(data) {
		c := data[i]

		// ESC — handle CSI / OSC / other sequences.
		if c == 0x1b {
			i = b.consumeEscape(data, i+1)
			continue
		}

		// Control characters.
		switch c {
		case '\n':
			b.commitPending()
			i++
			continue
		case '\r':
			b.pendingCol = 0
			i++
			continue
		case '\b':
			if b.pendingCol > 0 {
				b.pendingCol--
			}
			i++
			continue
		case '\t':
			// Tab to next multiple of 8.
			next := (b.pendingCol/8 + 1) * 8
			for b.pendingCol < next {
				b.writeRune(' ')
			}
			i++
			continue
		case 0x07: // BEL
			i++
			continue
		}

		// Other C0 control chars — ignore.
		if c < 0x20 || c == 0x7f {
			i++
			continue
		}

		// Decode a UTF-8 rune.
		r, size := utf8.DecodeRune(data[i:])
		if r == utf8.RuneError && size <= 1 {
			i++
			continue
		}
		b.writeRune(r)
		i += size
	}
}

// consumeEscape processes an escape sequence starting at i (just past ESC)
// and returns the index of the first byte after the sequence.
func (b *Buffer) consumeEscape(data []byte, i int) int {
	if i >= len(data) {
		return i
	}

	switch data[i] {
	case '[':
		// CSI — parameters followed by a final byte in 0x40..0x7e.
		i++
		paramStart := i
		for i < len(data) && !(data[i] >= 0x40 && data[i] <= 0x7e) {
			i++
		}
		if i >= len(data) {
			return i
		}
		params := string(data[paramStart:i])
		final := data[i]
		i++
		b.applyCSI(final, params)
		return i

	case ']':
		// OSC — terminated by BEL (0x07) or ST (ESC \).
		i++
		for i < len(data) {
			if data[i] == 0x07 {
				return i + 1
			}
			if data[i] == 0x1b && i+1 < len(data) && data[i+1] == '\\' {
				return i + 2
			}
			i++
		}
		return i

	case 'P', 'X', '^', '_':
		// DCS / SOS / PM / APC — terminated by ST.
		i++
		for i < len(data) {
			if data[i] == 0x1b && i+1 < len(data) && data[i+1] == '\\' {
				return i + 2
			}
			i++
		}
		return i

	default:
		// Single-byte escape (e.g. ESC c, ESC D).
		return i + 1
	}
}

// applyCSI applies the CSI sequences we care about for line assembly.
// Cursor moves, screen scrolls, etc. are ignored — the vt emulator
// handles those for display; scrollback only stores text + style.
func (b *Buffer) applyCSI(final byte, params string) {
	switch final {
	case 'K': // EL — Erase in Line
		n := parseFirstParam(params)
		switch n {
		case 0: // erase from cursor to end
			if b.pendingCol < len(b.pending) {
				b.pending = b.pending[:b.pendingCol]
			}
		case 1: // erase from start to cursor
			for j := 0; j < b.pendingCol && j < len(b.pending); j++ {
				b.pending[j] = styledRune{r: ' ', sgr: b.currentSGR}
			}
		case 2: // erase entire line
			b.pending = b.pending[:0]
			b.pendingCol = 0
		}
	case 'm': // SGR — Select Graphic Rendition
		b.handleSGR(params)
	}
}

// handleSGR updates the current SGR state from a CSI m parameter string.
//
// If the parameters contain a 0 (reset), the current state is replaced with
// the new sequence. Otherwise the new sequence is appended to the existing
// state so that progressive style additions (e.g. "\x1b[31m" then "\x1b[1m"
// to add bold to red) accumulate correctly.
func (b *Buffer) handleSGR(params string) {
	// "\x1b[m" has no parameters and is equivalent to "\x1b[0m" — a reset.
	if params == "" {
		b.currentSGR = ""
		return
	}

	hasReset := false
	for _, p := range strings.Split(params, ";") {
		// Empty param between semicolons defaults to 0 — also a reset.
		if p == "" || p == "0" || p == "00" {
			hasReset = true
			break
		}
	}

	seq := "\x1b[" + params + "m"

	if hasReset {
		// Pure reset collapses to empty state.
		if strings.Trim(params, "0;") == "" {
			b.currentSGR = ""
			return
		}
		// Reset + new attrs: the new sequence fully describes the state.
		b.currentSGR = seq
		return
	}

	// Additive — stack onto existing state.
	b.currentSGR += seq
}

// parseFirstParam parses the first numeric CSI parameter. Defaults to 0.
func parseFirstParam(params string) int {
	if params == "" {
		return 0
	}
	if idx := strings.IndexByte(params, ';'); idx >= 0 {
		params = params[:idx]
	}
	params = strings.TrimPrefix(params, "?")
	n := 0
	for _, r := range params {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r - '0')
	}
	return n
}

// writeRune writes r at the current pending column with the active SGR,
// extending the pending line with space-cells if needed.
func (b *Buffer) writeRune(r rune) {
	cell := styledRune{r: r, sgr: b.currentSGR}
	if b.pendingCol < len(b.pending) {
		b.pending[b.pendingCol] = cell
	} else {
		for len(b.pending) < b.pendingCol {
			b.pending = append(b.pending, styledRune{r: ' '})
		}
		b.pending = append(b.pending, cell)
	}
	b.pendingCol++
}

// commitPending serializes the pending cells, commits to the ring, and
// resets assembly state. Empty lines are still committed so blank lines
// in output are preserved.
func (b *Buffer) commitPending() {
	line := serializeCells(b.pending)
	b.commitLine(line)
	b.pending = b.pending[:0]
	b.pendingCol = 0
}

// commitLine writes a finished line into the ring buffer.
// Caller must hold b.mu.
func (b *Buffer) commitLine(line string) {
	b.lines[b.head] = line
	b.head = (b.head + 1) % b.capacity
	if b.count < b.capacity {
		b.count++
	}
	b.totalWritten++
}

// serializeCells renders a slice of styled cells as a string with minimal
// SGR transitions. Trailing space-cells are trimmed so blank padding does
// not carry the background color into the scrollback display.
func serializeCells(cells []styledRune) string {
	// Find the last non-space cell so we trim trailing whitespace padding.
	lastNonSpace := -1
	for i := len(cells) - 1; i >= 0; i-- {
		if cells[i].r != ' ' {
			lastNonSpace = i
			break
		}
	}
	if lastNonSpace < 0 {
		return ""
	}

	var b strings.Builder
	prev := ""
	for i := 0; i <= lastNonSpace; i++ {
		c := cells[i]
		if c.sgr != prev {
			// Reset before switching so the new style starts clean.
			if prev != "" {
				b.WriteString("\x1b[0m")
			}
			if c.sgr != "" {
				b.WriteString(c.sgr)
			}
			prev = c.sgr
		}
		b.WriteRune(c.r)
	}
	if prev != "" {
		b.WriteString("\x1b[0m")
	}
	return b.String()
}

// LineCount returns the number of committed lines currently in the buffer.
func (b *Buffer) LineCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.count
}

// TotalWritten returns the monotonic count of lines ever committed.
// Safe to use as a stable anchor across concurrent output — a captured
// value keeps pointing at the same logical position, even as new lines
// arrive (until the ring evicts it).
func (b *Buffer) TotalWritten() int64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.totalWritten
}

// OldestSerial returns the serial of the oldest line still in the buffer.
// Lines with serials below this have been evicted.
func (b *Buffer) OldestSerial() int64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.totalWritten - int64(b.count)
}

// Lines returns the last n committed lines in order. If n exceeds the
// buffer size, returns everything.
func (b *Buffer) Lines(n int) []string {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if n > b.count {
		n = b.count
	}
	if n <= 0 {
		return nil
	}

	result := make([]string, n)
	start := (b.head - n + b.capacity) % b.capacity
	for i := 0; i < n; i++ {
		idx := (start + i) % b.capacity
		result[i] = b.lines[idx]
	}
	return result
}

// AllLines returns every committed line currently in the buffer.
func (b *Buffer) AllLines() []string {
	return b.Lines(b.LineCount())
}

// LineAt returns the line at buffer position idx (0 = oldest in buffer).
func (b *Buffer) LineAt(idx int) string {
	b.mu.RLock()
	defer b.mu.RUnlock()

	if idx < 0 || idx >= b.count {
		return ""
	}
	start := (b.head - b.count + b.capacity) % b.capacity
	actual := (start + idx) % b.capacity
	return b.lines[actual]
}

// LineAtSerial returns the line with the given monotonic serial number.
// Returns ("", false) if the serial is outside the currently retained
// range (either evicted or not yet written).
func (b *Buffer) LineAtSerial(serial int64) (string, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	oldest := b.totalWritten - int64(b.count)
	if serial < oldest || serial >= b.totalWritten {
		return "", false
	}
	offsetFromOldest := int(serial - oldest)
	start := (b.head - b.count + b.capacity) % b.capacity
	idx := (start + offsetFromOldest) % b.capacity
	return b.lines[idx], true
}

// PendingLine returns the current partially-assembled line (with SGR),
// if any. Useful when a consumer wants to display the live state of a
// line being rewritten via \r (e.g. a progress bar) without waiting for
// its final \n.
func (b *Buffer) PendingLine() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return serializeCells(b.pending)
}
