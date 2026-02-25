package porttracker

import (
	"context"
	"sync"
	"time"
)

// Tracker coordinates port detection from both regex parsing and system scanning.
type Tracker struct {
	mu           sync.RWMutex
	regexPorts   map[int]bool
	systemPorts  map[int]ListeningPort
	combined     []ListeningPort
	cancel       context.CancelFunc
	scanInterval time.Duration
	onChange     func([]ListeningPort)
}

// New creates a new port Tracker.
func New(onChange func([]ListeningPort)) *Tracker {
	return &Tracker{
		regexPorts:   make(map[int]bool),
		systemPorts:  make(map[int]ListeningPort),
		scanInterval: 5 * time.Second,
		onChange:      onChange,
	}
}

// Start begins background scanning.
func (t *Tracker) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	t.cancel = cancel

	go t.scanLoop(ctx)
}

// Stop halts background scanning.
func (t *Tracker) Stop() {
	if t.cancel != nil {
		t.cancel()
	}
}

// FeedOutput processes terminal output for port detection.
func (t *Tracker) FeedOutput(data []byte) {
	ports := ParsePorts(string(data))
	if len(ports) == 0 {
		return
	}

	t.mu.Lock()
	changed := false
	for _, port := range ports {
		if !t.regexPorts[port] {
			t.regexPorts[port] = true
			changed = true
		}
	}
	t.mu.Unlock()

	if changed {
		t.recombine()
	}
}

// Ports returns the current detected ports.
func (t *Tracker) Ports() []ListeningPort {
	t.mu.RLock()
	defer t.mu.RUnlock()
	result := make([]ListeningPort, len(t.combined))
	copy(result, t.combined)
	return result
}

func (t *Tracker) scanLoop(ctx context.Context) {
	ticker := time.NewTicker(t.scanInterval)
	defer ticker.Stop()

	// Do an immediate scan
	t.doScan(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			t.doScan(ctx)
		}
	}
}

func (t *Tracker) doScan(ctx context.Context) {
	ports, err := ScanListeningPorts(ctx)
	if err != nil {
		return
	}

	t.mu.Lock()
	t.systemPorts = make(map[int]ListeningPort)
	for _, p := range ports {
		t.systemPorts[p.Port] = p
	}
	t.mu.Unlock()

	t.recombine()
}

func (t *Tracker) recombine() {
	t.mu.Lock()

	seen := make(map[int]bool)
	var combined []ListeningPort

	// System ports take priority (they have PID + process name)
	for port, lp := range t.systemPorts {
		seen[port] = true
		// Only include ports that are also detected via regex or are in common dev ranges
		if t.regexPorts[port] || isDevPort(port) {
			combined = append(combined, lp)
		}
	}

	// Add regex-detected ports not found in system scan
	for port := range t.regexPorts {
		if !seen[port] {
			combined = append(combined, ListeningPort{Port: port})
		}
	}

	t.combined = combined
	t.mu.Unlock()

	if t.onChange != nil {
		t.onChange(combined)
	}
}

func isDevPort(port int) bool {
	// Common development ports
	return (port >= 3000 && port <= 3999) ||
		(port >= 4000 && port <= 4999) ||
		(port >= 5000 && port <= 5999) ||
		(port >= 8000 && port <= 9999)
}
