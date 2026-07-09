// Package mkdir implements the `mkdir` builtin: create directories, with -p to
// create parents and ignore existing ones.
package mkdir

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/asc/sax/internal/builtins"
)

// Command is the mkdir builtin.
type Command struct{}

// New returns a ready-to-register mkdir command.
func New() *Command { return &Command{} }

// Name implements builtins.Command.
func (c *Command) Name() string { return "mkdir" }

// Run creates the named directories.
func (c *Command) Run(env *builtins.Env) int {
	parents := false
	var dirs []string
	for _, a := range env.Args {
		if strings.HasPrefix(a, "-") && a != "-" {
			for _, r := range a[1:] {
				if r == 'p' {
					parents = true
				} else {
					fmt.Fprintf(env.Stderr, "mkdir: invalid option -- '%c'\n", r)
					return 2
				}
			}
			continue
		}
		dirs = append(dirs, a)
	}

	if len(dirs) == 0 {
		fmt.Fprintln(env.Stderr, "mkdir: missing operand")
		return 1
	}

	code := 0
	for _, d := range dirs {
		path := d
		if !filepath.IsAbs(path) {
			path = filepath.Join(env.Cwd, d)
		}
		var err error
		if parents {
			err = os.MkdirAll(path, 0o755)
		} else {
			err = os.Mkdir(path, 0o755)
		}
		if err != nil {
			fmt.Fprintf(env.Stderr, "mkdir: cannot create directory '%s': %s\n", d, reason(err))
			code = 1
		}
	}
	return code
}

func reason(err error) string {
	switch {
	case os.IsExist(err):
		return "File exists"
	case os.IsNotExist(err):
		return "No such file or directory"
	case os.IsPermission(err):
		return "Permission denied"
	default:
		return err.Error()
	}
}
