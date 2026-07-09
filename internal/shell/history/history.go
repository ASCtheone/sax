// Package history stores the shell command history with optional file
// persistence and backward substring search. It is terminal-agnostic: the line
// editor drives navigation and search using these primitives.
package history

import (
	"bufio"
	"os"
	"strings"
)

// DefaultMax is the cap on retained entries when none is given.
const DefaultMax = 5000

// History is an ordered list of past command lines, oldest first.
type History struct {
	entries []string
	path    string
	max     int
}

// New returns a history backed by path (empty path = in-memory only).
func New(path string, max int) *History {
	if max <= 0 {
		max = DefaultMax
	}
	return &History{path: path, max: max}
}

// Load reads persisted entries from the backing file. A missing file is not an
// error.
func (h *History) Load() error {
	if h.path == "" {
		return nil
	}
	f, err := os.Open(h.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		if line := sc.Text(); line != "" {
			h.entries = append(h.entries, line)
		}
	}
	h.trim()
	return sc.Err()
}

// Add appends a command, skipping blanks and consecutive duplicates, and
// persists it to the backing file.
func (h *History) Add(cmd string) {
	cmd = strings.TrimRight(cmd, "\r\n")
	if strings.TrimSpace(cmd) == "" {
		return
	}
	if n := len(h.entries); n > 0 && h.entries[n-1] == cmd {
		return
	}
	h.entries = append(h.entries, cmd)
	h.trim()
	h.appendFile(cmd)
}

func (h *History) appendFile(cmd string) {
	if h.path == "" {
		return
	}
	f, err := os.OpenFile(h.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString(cmd + "\n")
}

func (h *History) trim() {
	if len(h.entries) > h.max {
		h.entries = append([]string(nil), h.entries[len(h.entries)-h.max:]...)
	}
}

// Len returns the number of entries.
func (h *History) Len() int { return len(h.entries) }

// At returns the entry at index i, or "" if out of range.
func (h *History) At(i int) string {
	if i < 0 || i >= len(h.entries) {
		return ""
	}
	return h.entries[i]
}

// SearchBackward returns the index of the most recent entry at or before `from`
// whose text contains query (case-insensitive), or -1 if none.
func (h *History) SearchBackward(query string, from int) int {
	if from >= len(h.entries) {
		from = len(h.entries) - 1
	}
	q := strings.ToLower(query)
	for i := from; i >= 0; i-- {
		if strings.Contains(strings.ToLower(h.entries[i]), q) {
			return i
		}
	}
	return -1
}
