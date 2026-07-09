// Package cat implements the `cat` builtin: write the contents of files to
// standard output.
package cat

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/asc/sax/internal/builtins"
)

// Command is the cat builtin.
type Command struct{}

// New returns a ready-to-register cat command.
func New() *Command { return &Command{} }

// Name implements builtins.Command.
func (c *Command) Name() string { return "cat" }

// Run concatenates the named files to stdout. (Reading from stdin will come
// with pipe support; for now at least one file is required.)
func (c *Command) Run(env *builtins.Env) int {
	if len(env.Args) == 0 {
		fmt.Fprintln(env.Stderr, "cat: no input files")
		return 1
	}

	code := 0
	for _, arg := range env.Args {
		path := arg
		if !filepath.IsAbs(path) {
			path = filepath.Join(env.Cwd, arg)
		}

		info, err := os.Stat(path)
		if err != nil {
			fmt.Fprintf(env.Stderr, "cat: %s: %s\n", arg, reason(err))
			code = 1
			continue
		}
		if info.IsDir() {
			fmt.Fprintf(env.Stderr, "cat: %s: Is a directory\n", arg)
			code = 1
			continue
		}

		f, err := os.Open(path)
		if err != nil {
			fmt.Fprintf(env.Stderr, "cat: %s: %s\n", arg, reason(err))
			code = 1
			continue
		}
		_, err = io.Copy(env.Stdout, f)
		f.Close()
		if err != nil {
			fmt.Fprintf(env.Stderr, "cat: %s: %v\n", arg, err)
			code = 1
		}
	}
	return code
}

func reason(err error) string {
	switch {
	case os.IsNotExist(err):
		return "No such file or directory"
	case os.IsPermission(err):
		return "Permission denied"
	default:
		return err.Error()
	}
}
