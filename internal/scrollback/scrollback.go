package scrollback

import (
	"strings"
	"sync"
)

const DefaultCapacity = 10000

// Buffer is a ring buffer that stores scrollback lines.
type Buffer struct {
	lines    []string
	head     int // next write position
	count    int
	capacity int
	mu       sync.RWMutex
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

// AppendLine adds a line to the buffer.
func (b *Buffer) AppendLine(line string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lines[b.head] = line
	b.head = (b.head + 1) % b.capacity
	if b.count < b.capacity {
		b.count++
	}
}

// AppendOutput processes raw terminal output and splits into lines.
func (b *Buffer) AppendOutput(data []byte) {
	text := string(data)
	// Strip ANSI escape sequences for scrollback storage
	text = stripANSI(text)
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		if line != "" {
			b.AppendLine(line)
		}
	}
}

// LineCount returns the number of lines in the buffer.
func (b *Buffer) LineCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.count
}

// Lines returns the last n lines from the buffer. If n > count, returns all lines.
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

// AllLines returns all lines in order.
func (b *Buffer) AllLines() []string {
	return b.Lines(b.LineCount())
}

// LineAt returns the line at the given index (0 = oldest).
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

// stripANSI removes ANSI escape sequences from text.
func stripANSI(s string) string {
	var result strings.Builder
	result.Grow(len(s))
	i := 0
	for i < len(s) {
		if s[i] == '\x1b' {
			// Skip ESC sequence
			i++
			if i < len(s) && s[i] == '[' {
				i++
				for i < len(s) && !((s[i] >= 'A' && s[i] <= 'Z') || (s[i] >= 'a' && s[i] <= 'z')) {
					i++
				}
				if i < len(s) {
					i++ // skip final letter
				}
			} else if i < len(s) && s[i] == ']' {
				// OSC sequence — skip until ST (ESC \ or BEL)
				i++
				for i < len(s) {
					if s[i] == '\x07' {
						i++
						break
					}
					if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '\\' {
						i += 2
						break
					}
					i++
				}
			} else if i < len(s) {
				i++ // skip single char after ESC
			}
		} else {
			result.WriteByte(s[i])
			i++
		}
	}
	return result.String()
}
