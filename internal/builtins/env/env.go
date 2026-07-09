// Package env implements the `env` builtin: print the environment, one
// KEY=VALUE per line, sorted.
package env

import (
	"fmt"
	"os"
	"sort"

	"github.com/asc/sax/internal/builtins"
)

// Command is the env builtin.
type Command struct{}

// New returns a ready-to-register env command.
func New() *Command { return &Command{} }

// Name implements builtins.Command.
func (c *Command) Name() string { return "env" }

// Run prints the environment. (Setting variables / running a command will come
// once the shell tracks its own environment.)
func (c *Command) Run(e *builtins.Env) int {
	if len(e.Args) > 0 {
		fmt.Fprintln(e.Stderr, "env: setting variables is not yet supported")
		return 2
	}
	vars := os.Environ()
	sort.Strings(vars)
	for _, v := range vars {
		fmt.Fprintln(e.Stdout, v)
	}
	return 0
}
