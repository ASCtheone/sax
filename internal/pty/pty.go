package pty

import (
	"io"
	"os"
	"os/exec"
	"sync"

	gopty "github.com/aymanbagabas/go-pty"
)

// Process wraps a pseudo-terminal with a running shell process.
type Process struct {
	pty gopty.Pty
	cmd *gopty.Cmd
	mu  sync.Mutex
}

// Start spawns a new PTY with the detected shell.
func Start(cols, rows int, workDir string) (*Process, error) {
	shell, args := DetectShell()
	return startProcess(cols, rows, shell, args[1:], workDir, nil)
}

// StartCommand spawns a new PTY running a specific command.
func StartCommand(cols, rows int, name string, args []string, workDir string) (*Process, error) {
	return startProcess(cols, rows, name, args, workDir, nil)
}

// StartCommandEnv is StartCommand with extra "KEY=VALUE" variables for this
// process only; they override the daemon-wide environment.
func StartCommandEnv(cols, rows int, name string, args []string, workDir string, env []string) (*Process, error) {
	return startProcess(cols, rows, name, args, workDir, env)
}

func startProcess(cols, rows int, name string, args []string, workDir string, env []string) (*Process, error) {
	// Resolve command path (handles .cmd, .bat, .exe on Windows)
	if resolved, err := exec.LookPath(name); err == nil {
		name = resolved
	}
	if len(wrapper) > 0 {
		args = append(append(append([]string{}, wrapper[1:]...), name), args...)
		name = wrapper[0]
	}

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
	// Inject environment variables configured in ~/.saxrc (setenv/export),
	// then this process's own.
	cmd.Env = append(cmd.Env, extraEnv...)
	cmd.Env = append(cmd.Env, env...)
	if workDir != "" {
		cmd.Dir = workDir
	}

	if err := cmd.Start(); err != nil {
		p.Close()
		return nil, err
	}
	// The child has its own copy of the slave. Keeping ours open would stop
	// master reads from ever failing on Linux, so a command's exit would go
	// unnoticed and its session would never close.
	if up, ok := p.(gopty.UnixPty); ok {
		_ = up.Slave().Close()
	}

	return &Process{pty: p, cmd: cmd}, nil
}

// Wait blocks until the underlying process exits, returning its exit error
// (nil on a clean exit). Detecting exit this way is reliable across platforms,
// unlike waiting for the PTY to reach EOF — on Windows ConPTY the master does
// not always close when the child exits.
func (p *Process) Wait() error {
	if p.cmd == nil {
		return nil
	}
	return p.cmd.Wait()
}

// ExitCode returns the process exit code, or -1 if it is not yet available.
// Call after Wait.
func (p *Process) ExitCode() int {
	if p.cmd == nil || p.cmd.ProcessState == nil {
		return -1
	}
	return p.cmd.ProcessState.ExitCode()
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

// Pid returns the process ID of the command, or 0 before it started.
func (p *Process) Pid() int {
	if p.cmd == nil || p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

// Fd returns the file descriptor of the PTY master, if available.
func (p *Process) Fd() uintptr {
	if f, ok := p.pty.(interface{ Fd() uintptr }); ok {
		return f.Fd()
	}
	return 0
}
