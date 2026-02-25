package terminal

import (
	"sync"

	"github.com/asc/sax/internal/scrollback"
	"github.com/charmbracelet/x/vt"
)

// Terminal wraps a vt.SafeEmulator providing thread-safe terminal emulation.
type Terminal struct {
	emu        *vt.SafeEmulator
	mu         sync.RWMutex
	cols       int
	rows       int
	Scrollback *scrollback.Buffer
}

// New creates a new Terminal with the given dimensions.
func New(cols, rows int) *Terminal {
	emu := vt.NewSafeEmulator(cols, rows)
	return &Terminal{
		emu:        emu,
		cols:       cols,
		rows:       rows,
		Scrollback: scrollback.NewBuffer(scrollback.DefaultCapacity),
	}
}

// Write feeds raw bytes from the PTY into the terminal emulator
// and captures output into the scrollback buffer.
func (t *Terminal) Write(p []byte) (int, error) {
	// Feed to scrollback
	t.Scrollback.AppendOutput(p)
	return t.emu.Write(p)
}

// Render returns the ANSI-encoded string representation of the terminal screen.
func (t *Terminal) Render() string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.emu.Render()
}

// Resize updates the terminal dimensions.
func (t *Terminal) Resize(cols, rows int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cols = cols
	t.rows = rows
	t.emu.Resize(cols, rows)
}

// Cols returns the current column count.
func (t *Terminal) Cols() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.cols
}

// Rows returns the current row count.
func (t *Terminal) Rows() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.rows
}
