package shell

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"time"

	"github.com/asc/sax/internal/pty"

	"golang.org/x/term"
)

// runInteractive drives the REPL on a real terminal. The terminal is held in
// raw mode for the whole session so ss owns input handling uniformly — whether
// it runs in a console, a ConPTY, or inside sax. A single goroutine pumps the
// terminal's bytes into one channel; at the prompt those bytes drive a line
// editor, and while a child program runs they are forwarded to it. One reader
// on the terminal is what stops the prompt and a child from stealing each
// other's keystrokes.
//
// Ctrl+C arrives as a 0x03 byte (raw mode emits no console interrupt): at the
// prompt the editor cancels the line; while a child runs the byte is forwarded
// so the program (or its pager/help screen) breaks and control returns to ss.
func (s *Shell) runInteractive(in, out *os.File) error {
	oldState, err := term.MakeRaw(int(in.Fd()))
	if err != nil {
		// Can't control the terminal — fall back to the simple line loop.
		return s.runBatch()
	}
	defer func() { _ = term.Restore(int(in.Fd()), oldState) }()

	stop := ignoreInterrupts()
	defer stop()

	// Raw mode does no echo or newline translation, so ss owns both: its own
	// text goes through a CRLF-translating writer; child output (already proper
	// CRLF from its PTY) is written to the raw file directly via runPTY.
	s.out = crlfWriter{out}
	s.errOut = crlfWriter{s.errOut}

	// Builtins can now query live terminal width and emit color.
	s.termOut = out
	s.color = true

	_ = s.history.Load()

	input := make(chan []byte, 16)
	go pumpInput(in, input)

	ed := newEditor(s.out, s.history)
	ed.start(s.prompt())

	for !s.exiting {
		chunk, ok := <-input
		if !ok {
			io.WriteString(s.out, "\n")
			return nil // terminal closed (EOF)
		}
		for _, b := range chunk {
			action, line := ed.feed(b)
			switch action {
			case edSubmit:
				io.WriteString(s.out, "\n")
				s.history.Add(line)
				s.evalInteractive(line, input, out)
				if s.exiting {
					return nil
				}
				ed.start(s.prompt())
			case edCancel:
				io.WriteString(s.out, "^C\n")
				ed.start(s.prompt())
			case edEOF:
				io.WriteString(s.out, "\n")
				return nil
			case edNone:
			}
		}
	}
	return nil
}

// ignoreInterrupts subscribes to os.Interrupt and swallows it, suppressing the
// default action of killing ss. The real Ctrl+C handling happens via the 0x03
// byte in raw mode; this is a safety net for any console interrupt that still
// slips through (e.g. Ctrl+Break). The returned func unsubscribes.
func ignoreInterrupts() func() {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			case <-sigCh:
				// swallow
			}
		}
	}()
	return func() {
		signal.Stop(sigCh)
		close(done)
	}
}

// evalInteractive parses one line and dispatches it. Builtins run in-process;
// external programs run in a PTY so full-screen, interactive tools work.
func (s *Shell) evalInteractive(line string, input <-chan []byte, out *os.File) {
	args, err := Parse(line)
	if err != nil {
		fmt.Fprintf(s.errOut, "ss: %v\n", err)
		return
	}
	if len(args) == 0 {
		return
	}
	if s.runBuiltin(args) {
		return
	}
	s.runPTY(args, input, out)
}

// runPTY launches a program in a pseudo-terminal and shuttles bytes between the
// terminal and the child until the child exits. The session terminal is already
// in raw mode, so keystrokes (including Ctrl+C as 0x03) flow straight through
// the shared pump to the child. Child output is written to the raw terminal.
func (s *Shell) runPTY(args []string, input <-chan []byte, out *os.File) {
	// Resolve up front (cwd-first, then PATH, with extension inference) so a
	// missing command produces a clean message rather than a failed PTY spawn,
	// and so cwd programs run without a ./ prefix.
	path, ok := s.resolveCommand(args[0])
	if !ok {
		fmt.Fprintf(s.errOut, "ss: command not found: %s\n", args[0])
		s.lastErr = 127
		return
	}

	cols, rows, err := term.GetSize(int(out.Fd()))
	if err != nil || cols == 0 || rows == 0 {
		cols, rows = 80, 24
	}

	name, spawnArgs := spawnCommand(path, args[1:])
	proc, err := pty.StartCommand(cols, rows, name, spawnArgs, s.cwd)
	if err != nil {
		fmt.Fprintf(s.errOut, "ss: %v\n", err)
		s.lastErr = 1
		return
	}

	// Child output -> our terminal. On Windows ConPTY this copy may not reach
	// EOF when the child exits, so it is NOT used to detect exit; we wait on
	// the process instead and use this only to stream output.
	outDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(out, proc)
		close(outDone)
	}()

	// Authoritative exit signal: the process itself.
	exitDone := make(chan struct{})
	go func() {
		_ = proc.Wait()
		close(exitDone)
	}()

	finish := func() {
		// Let the output pump flush any output produced just before exit, then
		// close the PTY to unblock it (Close makes the pending Read return).
		select {
		case <-outDone:
		case <-time.After(flushGrace):
		}
		_ = proc.Close()
		<-outDone
		s.lastErr = proc.ExitCode()
	}

	for {
		select {
		case <-exitDone:
			finish()
			return
		case chunk, ok := <-input:
			if !ok {
				finish()
				return
			}
			if _, err := proc.Write(chunk); err != nil {
				finish()
				return
			}
		}
	}
}

// flushGrace is how long runPTY waits for trailing child output to drain after
// the process exits before forcibly closing the PTY.
const flushGrace = 100 * time.Millisecond

// pumpInput reads the terminal continuously and forwards copies of each read to
// ch, closing it on EOF. Running for the shell's lifetime, it is the only
// reader of the terminal.
func pumpInput(r io.Reader, ch chan<- []byte) {
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			b := make([]byte, n)
			copy(b, buf[:n])
			ch <- b
		}
		if err != nil {
			close(ch)
			return
		}
	}
}
