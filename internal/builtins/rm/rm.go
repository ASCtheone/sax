// Package rm implements the `rm` builtin: remove files and (with -r)
// directories, with -f to ignore missing paths.
package rm

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/asc/sax/internal/builtins"
)

// Command is the rm builtin.
type Command struct{}

// New returns a ready-to-register rm command.
func New() *Command { return &Command{} }

// Name implements builtins.Command.
func (c *Command) Name() string { return "rm" }

// Run removes the named paths.
func (c *Command) Run(env *builtins.Env) int {
	recursive, force := false, false
	var paths []string
	for _, a := range env.Args {
		if strings.HasPrefix(a, "-") && a != "-" {
			for _, r := range a[1:] {
				switch r {
				case 'r', 'R':
					recursive = true
				case 'f':
					force = true
				default:
					fmt.Fprintf(env.Stderr, "rm: invalid option -- '%c'\n", r)
					return 2
				}
			}
			continue
		}
		paths = append(paths, a)
	}

	if len(paths) == 0 {
		if force {
			return 0
		}
		fmt.Fprintln(env.Stderr, "rm: missing operand")
		return 1
	}

	code := 0
	for _, p := range paths {
		path := p
		if !filepath.IsAbs(path) {
			path = filepath.Join(env.Cwd, p)
		}

		info, err := os.Lstat(path)
		if err != nil {
			if force {
				continue // -f ignores missing paths
			}
			fmt.Fprintf(env.Stderr, "rm: cannot remove '%s': %s\n", p, reason(err))
			code = 1
			continue
		}
		if info.IsDir() && !recursive {
			fmt.Fprintf(env.Stderr, "rm: cannot remove '%s': Is a directory\n", p)
			code = 1
			continue
		}

		if recursive {
			err = os.RemoveAll(path)
		} else {
			err = os.Remove(path)
		}
		if err != nil {
			fmt.Fprintf(env.Stderr, "rm: cannot remove '%s': %s\n", p, reason(err))
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
