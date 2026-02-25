package pty

import (
	"io"
	"os"
	"sync"

	gopty "github.com/aymanbagabas/go-pty"
)

// Process wraps a pseudo-terminal with a running shell process.
type Process struct {
	pty gopty.Pty
	mu  sync.Mutex
}

// Start spawns a new PTY with the detected shell.
func Start(cols, rows int) (*Process, error) {
	shell, args := DetectShell()
	return startProcess(cols, rows, shell, args[1:])
}

// StartCommand spawns a new PTY running a specific command.
func StartCommand(cols, rows int, name string, args []string) (*Process, error) {
	return startProcess(cols, rows, name, args)
}

func startProcess(cols, rows int, name string, args []string) (*Process, error) {
	p, err := gopty.New()
	if err != nil {
		return nil, err
	}

	if err := p.Resize(cols, rows); err != nil {
		p.Close()
		return nil, err
	}

	cmd := p.Command(name, args...)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")

	if err := cmd.Start(); err != nil {
		p.Close()
		return nil, err
	}

	return &Process{pty: p}, nil
}

// Read reads output from the PTY.
func (p *Process) Read(buf []byte) (int, error) {
	return p.pty.Read(buf)
}

// Write sends input to the PTY.
func (p *Process) Write(data []byte) (int, error) {
	return p.pty.Write(data)
}

// WriteString sends a string to the PTY.
func (p *Process) WriteString(s string) (int, error) {
	return io.WriteString(p.pty, s)
}

// Resize updates the PTY dimensions.
func (p *Process) Resize(cols, rows int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.pty.Resize(cols, rows)
}

// Close terminates the PTY and process.
func (p *Process) Close() error {
	return p.pty.Close()
}

// Fd returns the file descriptor of the PTY master, if available.
func (p *Process) Fd() uintptr {
	if f, ok := p.pty.(interface{ Fd() uintptr }); ok {
		return f.Fd()
	}
	return 0
}
