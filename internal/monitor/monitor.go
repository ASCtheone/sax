package monitor

import (
	"sync"
	"time"
)

// DefaultSilenceTimeout is the default silence detection period.
const DefaultSilenceTimeout = 30 * time.Second

// Alert represents a monitoring alert.
type Alert struct {
	PaneID string
	Type   string // "activity" or "silence"
}

// Monitor tracks activity and silence for panes.
type Monitor struct {
	mu             sync.Mutex
	activityPanes  map[string]bool      // paneID -> monitoring enabled
	silencePanes   map[string]bool      // paneID -> monitoring enabled
	lastActivity   map[string]time.Time // paneID -> last activity timestamp
	silenceTimeout time.Duration
	alertCh        chan Alert
	done           chan struct{}
}

// New creates a new Monitor.
func New(alertCh chan Alert) *Monitor {
	return &Monitor{
		activityPanes:  make(map[string]bool),
		silencePanes:   make(map[string]bool),
		lastActivity:   make(map[string]time.Time),
		silenceTimeout: DefaultSilenceTimeout,
		alertCh:        alertCh,
		done:           make(chan struct{}),
	}
}

// Start begins the silence detection loop.
func (m *Monitor) Start() {
	go m.silenceLoop()
}

// Stop halts the monitor.
func (m *Monitor) Stop() {
	select {
	case <-m.done:
	default:
		close(m.done)
	}
}

// ToggleActivity toggles activity monitoring for a pane. Returns new state.
func (m *Monitor) ToggleActivity(paneID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.activityPanes[paneID] = !m.activityPanes[paneID]
	return m.activityPanes[paneID]
}

// ToggleSilence toggles silence monitoring for a pane. Returns new state.
func (m *Monitor) ToggleSilence(paneID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.silencePanes[paneID] = !m.silencePanes[paneID]
	if m.silencePanes[paneID] {
		m.lastActivity[paneID] = time.Now()
	}
	return m.silencePanes[paneID]
}

// NotifyActivity should be called when a pane receives output.
// It checks if activity monitoring is enabled and sends alerts.
func (m *Monitor) NotifyActivity(paneID string, isBackground bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.lastActivity[paneID] = time.Now()

	if isBackground && m.activityPanes[paneID] {
		select {
		case m.alertCh <- Alert{PaneID: paneID, Type: "activity"}:
		default:
		}
	}
}

// IsActivityMonitored returns whether a pane has activity monitoring.
func (m *Monitor) IsActivityMonitored(paneID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.activityPanes[paneID]
}

// IsSilenceMonitored returns whether a pane has silence monitoring.
func (m *Monitor) IsSilenceMonitored(paneID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.silencePanes[paneID]
}

// RemovePane removes all monitoring for a pane.
func (m *Monitor) RemovePane(paneID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.activityPanes, paneID)
	delete(m.silencePanes, paneID)
	delete(m.lastActivity, paneID)
}

func (m *Monitor) silenceLoop() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-m.done:
			return
		case <-ticker.C:
			m.checkSilence()
		}
	}
}

func (m *Monitor) checkSilence() {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	for paneID, enabled := range m.silencePanes {
		if !enabled {
			continue
		}
		last, ok := m.lastActivity[paneID]
		if !ok {
			continue
		}
		if now.Sub(last) >= m.silenceTimeout {
			select {
			case m.alertCh <- Alert{PaneID: paneID, Type: "silence"}:
			default:
			}
			// Reset to avoid repeated alerts
			m.lastActivity[paneID] = now
		}
	}
}
