// Package shell implements the sax shell (ss): a minimal, embeddable
// read-eval-print loop that parses a command line, dispatches to a builtin, or
// launches an external program with its I/O wired to the shell's terminal.
//
// State (working directory, last exit code) lives on the Shell struct rather
// than mutating process-global state, keeping the shell self-contained and
// testable. This POC is the seam everything else grows from: pipes,
// redirection, job control, and an eventual scripting language all hang off
// eval/runExternal.
package shell

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/asc/sax/internal/builtins"
	"github.com/asc/sax/internal/shell/history"
	"github.com/asc/sax/internal/shell/prompt"

	"golang.org/x/term"
)

// Shell is a single interactive session: streams plus mutable session state.
type Shell struct {
	in       io.Reader
	out      io.Writer
	errOut   io.Writer
	cwd      string
	lastErr  int
	exiting  bool
	builtins *builtins.Registry
	history  *history.History
	termOut  *os.File // set while interactive, for live terminal-size queries
	color    bool     // true while interactive (ANSI output appropriate)
}

// New creates a Shell wired to the given streams, starting in dir. When dir is
// empty it defaults to the process working directory.
func New(in io.Reader, out, errOut io.Writer, dir string) (*Shell, error) {
	if dir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("resolve working dir: %w", err)
		}
		dir = wd
	}
	return &Shell{
		in:       in,
		out:      out,
		errOut:   errOut,
		cwd:      filepath.Clean(dir),
		builtins: builtins.NewRegistry(),
		history:  history.New(defaultHistoryPath(), 0),
	}, nil
}

// defaultHistoryPath is $SS_HISTFILE when set, otherwise ~/.ss_history, or ""
// when the home dir is unknown (history then stays in-memory only).
func defaultHistoryPath() string {
	if p := os.Getenv("SS_HISTFILE"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".ss_history")
}

// Register adds feature builtins (one package each) to the shell. Wiring lives
// in the composition root (main), so the shell core stays decoupled from
// individual features.
func (s *Shell) Register(cmds ...builtins.Command) {
	for _, c := range cmds {
		s.builtins.Register(c)
	}
}

// Run executes the read-eval-print loop until EOF or the `exit` builtin. When
// stdin and stdout are both real terminals it runs the interactive engine
// (PTY-backed children, raw-mode passthrough); otherwise it falls back to the
// batch loop, which keeps piped/redirected use (and tests) simple and proven.
func (s *Shell) Run() error {
	if in, out, ok := s.terminalFiles(); ok {
		return s.runInteractive(in, out)
	}
	return s.runBatch()
}

// terminalFiles reports whether both streams are *os.File terminals, returning
// them for raw-mode and size queries when so.
func (s *Shell) terminalFiles() (in, out *os.File, ok bool) {
	inFile, inOK := s.in.(*os.File)
	outFile, outOK := s.out.(*os.File)
	if !inOK || !outOK {
		return nil, nil, false
	}
	if !term.IsTerminal(int(inFile.Fd())) || !term.IsTerminal(int(outFile.Fd())) {
		return nil, nil, false
	}
	return inFile, outFile, true
}

// runBatch is the line-at-a-time loop used for non-interactive input.
func (s *Shell) runBatch() error {
	reader := bufio.NewReader(s.in)
	for !s.exiting {
		fmt.Fprint(s.out, s.prompt())
		line, err := reader.ReadString('\n')
		if len(line) > 0 {
			s.eval(line)
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				fmt.Fprintln(s.out)
				return nil
			}
			return fmt.Errorf("read input: %w", err)
		}
	}
	return nil
}

// prompt renders the prompt string (zsh "robbyrussell" style) for the current
// directory and last exit code.
func (s *Shell) prompt() string {
	return prompt.Render(s.cwd, s.lastErr, s.color)
}

// eval parses and dispatches a single input line.
func (s *Shell) eval(line string) {
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
	s.runExternal(args)
}

// runBuiltin handles commands implemented in-process. It returns true when the
// command was a builtin (handled), false to fall through to external exec.
func (s *Shell) runBuiltin(args []string) bool {
	switch args[0] {
	case "exit", "quit":
		s.exiting = true
		return true
	case "cd":
		s.builtinCd(args[1:])
		return true
	case "pwd":
		fmt.Fprintln(s.out, s.cwd)
		return true
	}

	// Feature builtins (ls, etc.) registered from their own packages.
	if cmd, ok := s.builtins.Lookup(args[0]); ok {
		s.lastErr = cmd.Run(&builtins.Env{
			Cwd:    s.cwd,
			Args:   args[1:],
			Stdout: s.out,
			Stderr: s.errOut,
			Width:  s.terminalWidth(),
			Color:  s.color,
		})
		return true
	}
	return false
}

// terminalWidth returns the current terminal width in columns, or 0 when not
// attached to a terminal (e.g. batch mode).
func (s *Shell) terminalWidth() int {
	if s.termOut == nil {
		return 0
	}
	w, _, err := term.GetSize(int(s.termOut.Fd()))
	if err != nil {
		return 0
	}
	return w
}

// builtinCd changes the shell's working directory. With no argument it goes to
// the user's home directory.
func (s *Shell) builtinCd(args []string) {
	target := ""
	if len(args) == 0 {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintf(s.errOut, "cd: %v\n", err)
			return
		}
		target = home
	} else {
		target = args[0]
	}

	if !filepath.IsAbs(target) {
		target = filepath.Join(s.cwd, target)
	}
	info, err := os.Stat(target)
	if err != nil {
		fmt.Fprintf(s.errOut, "cd: %v\n", err)
		return
	}
	if !info.IsDir() {
		fmt.Fprintf(s.errOut, "cd: not a directory: %s\n", target)
		return
	}
	s.cwd = filepath.Clean(target)
}

// runExternal launches a program, wiring its standard streams to the shell and
// recording the exit code. The child runs in the shell's working directory so
// `cd` behaves without mutating the host process.
func (s *Shell) runExternal(args []string) {
	path, ok := s.resolveCommand(args[0])
	if !ok {
		fmt.Fprintf(s.errOut, "ss: command not found: %s\n", args[0])
		s.lastErr = 127
		return
	}

	name, spawnArgs := spawnCommand(path, args[1:])
	cmd := exec.Command(name, spawnArgs...)
	cmd.Dir = s.cwd
	cmd.Stdin = s.in
	cmd.Stdout = s.out
	cmd.Stderr = s.errOut
	cmd.Env = os.Environ()

	err := cmd.Run()
	if err == nil {
		s.lastErr = 0
		return
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		s.lastErr = exitErr.ExitCode()
		return
	}
	fmt.Fprintf(s.errOut, "ss: %v\n", err)
	s.lastErr = 1
}
