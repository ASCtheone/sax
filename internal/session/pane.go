package session

import (
	"fmt"
	"sync/atomic"

	"github.com/asc/sax/internal/pty"
	"github.com/asc/sax/internal/terminal"
)

var paneCounter atomic.Uint64

func nextPaneID() string {
	return fmt.Sprintf("pane-%d", paneCounter.Add(1))
}

// Pane represents a single terminal pane with its own PTY and emulator.
type Pane struct {
	ID       string
	Term     *terminal.Terminal
	Pty      *pty.Process
	Title    string
	ExitErr  error
	HasExited bool
}

// NewPane creates a new Pane and starts a PTY in it.
func NewPane(cols, rows int, workDir string) (*Pane, error) {
	id := nextPaneID()

	proc, err := pty.Start(cols, rows, workDir)
	if err != nil {
		return nil, fmt.Errorf("start pty: %w", err)
	}

	term := terminal.New(cols, rows)

	return &Pane{
		ID:    id,
		Term:  term,
		Pty:   proc,
		Title: "shell",
	}, nil
}

// NewPaneWithCommand creates a new Pane running a specific command.
func NewPaneWithCommand(cols, rows int, name string, args []string, workDir string) (*Pane, error) {
	id := nextPaneID()

	proc, err := pty.StartCommand(cols, rows, name, args, workDir)
	if err != nil {
		return nil, fmt.Errorf("start command: %w", err)
	}

	term := terminal.New(cols, rows)

	return &Pane{
		ID:    id,
		Term:  term,
		Pty:   proc,
		Title: name,
	}, nil
}

// Resize updates both the terminal emulator and the PTY.
func (p *Pane) Resize(cols, rows int) {
	if cols < 1 || rows < 1 {
		return
	}
	p.Term.Resize(cols, rows)
	_ = p.Pty.Resize(cols, rows)
}

// Close shuts down the pane's PTY.
func (p *Pane) Close() {
	if p.Pty != nil {
		_ = p.Pty.Close()
	}
}

// Render returns the ANSI representation of the terminal content.
func (p *Pane) Render() string {
	return p.Term.Render()
}
