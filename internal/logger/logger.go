package logger

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/asc/sax/internal/ipc"
)

// PaneLogger manages per-pane output logging to files.
type PaneLogger struct {
	mu      sync.Mutex
	files   map[string]*os.File // paneID -> log file
	enabled map[string]bool
}

// New creates a new PaneLogger.
func New() *PaneLogger {
	return &PaneLogger{
		files:   make(map[string]*os.File),
		enabled: make(map[string]bool),
	}
}

// Toggle enables or disables logging for a pane. Returns the new state.
func (pl *PaneLogger) Toggle(paneID string) bool {
	pl.mu.Lock()
	defer pl.mu.Unlock()

	if pl.enabled[paneID] {
		// Disable
		if f, ok := pl.files[paneID]; ok {
			f.Close()
			delete(pl.files, paneID)
		}
		pl.enabled[paneID] = false
		return false
	}

	// Enable
	dir := ipc.LogsDir()
	os.MkdirAll(dir, 0700)
	ts := time.Now().Format("20060102-150405")
	path := filepath.Join(dir, fmt.Sprintf("%s-%s.log", paneID, ts))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return false
	}
	pl.files[paneID] = f
	pl.enabled[paneID] = true
	return true
}

// IsEnabled returns whether logging is enabled for a pane.
func (pl *PaneLogger) IsEnabled(paneID string) bool {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	return pl.enabled[paneID]
}

// Write writes data to the log file for a pane, if logging is enabled.
func (pl *PaneLogger) Write(paneID string, data []byte) {
	pl.mu.Lock()
	f, ok := pl.files[paneID]
	enabled := pl.enabled[paneID]
	pl.mu.Unlock()

	if !ok || !enabled {
		return
	}
	f.Write(data)
}

// Close closes all open log files.
func (pl *PaneLogger) Close() {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	for _, f := range pl.files {
		f.Close()
	}
	pl.files = make(map[string]*os.File)
	pl.enabled = make(map[string]bool)
}

// Hardcopy dumps the rendered content of a pane to a file.
func Hardcopy(content string) (string, error) {
	dir := ipc.HardcopyDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	ts := time.Now().Format("20060102-150405")
	path := filepath.Join(dir, fmt.Sprintf("hardcopy-%s.txt", ts))
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		return "", err
	}
	return path, nil
}
