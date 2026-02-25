package app

// PtyOutputMsg signals that a pane's terminal has new output.
type PtyOutputMsg struct {
	PaneID string
}

// PtyExitMsg signals that a pane's PTY process has exited.
type PtyExitMsg struct {
	PaneID string
	Err    error
}

// PortUpdateMsg carries updated port information.
type PortUpdateMsg struct {
	Ports []PortInfo
}

// PortInfo represents a detected listening port.
type PortInfo struct {
	Port    int
	PID     int
	Process string
}

// ClearPrefixMsg clears prefix mode after timeout.
type ClearPrefixMsg struct{}
