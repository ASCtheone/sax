// Package echo implements the `echo` builtin: print arguments separated by
// spaces, with optional -n (no trailing newline) and -e (interpret backslash
// escapes).
package echo

import (
	"fmt"
	"strings"

	"github.com/asc/sax/internal/builtins"
)

// Command is the echo builtin.
type Command struct{}

// New returns a ready-to-register echo command.
func New() *Command { return &Command{} }

// Name implements builtins.Command.
func (c *Command) Name() string { return "echo" }

// Run prints the arguments.
func (c *Command) Run(env *builtins.Env) int {
	args := env.Args
	noNewline, interpret := false, false

	// Consume leading flag words made up only of n/e/E (e.g. -n, -ne).
	i := 0
	for ; i < len(args); i++ {
		a := args[i]
		if len(a) < 2 || a[0] != '-' || !onlyFlagChars(a[1:]) {
			break
		}
		for _, r := range a[1:] {
			switch r {
			case 'n':
				noNewline = true
			case 'e':
				interpret = true
			case 'E':
				interpret = false
			}
		}
	}

	out := strings.Join(args[i:], " ")
	if interpret {
		out = expandEscapes(out)
	}
	if noNewline {
		fmt.Fprint(env.Stdout, out)
	} else {
		fmt.Fprintln(env.Stdout, out)
	}
	return 0
}

func onlyFlagChars(s string) bool {
	for _, r := range s {
		if r != 'n' && r != 'e' && r != 'E' {
			return false
		}
	}
	return len(s) > 0
}

// expandEscapes interprets common C-style backslash escapes (-e).
func expandEscapes(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 >= len(s) {
			b.WriteByte(s[i])
			continue
		}
		i++
		switch s[i] {
		case 'n':
			b.WriteByte('\n')
		case 't':
			b.WriteByte('\t')
		case 'r':
			b.WriteByte('\r')
		case '\\':
			b.WriteByte('\\')
		case 'a':
			b.WriteByte(7)
		case 'b':
			b.WriteByte(8)
		case 'f':
			b.WriteByte(12)
		case 'v':
			b.WriteByte(11)
		case 'e':
			b.WriteByte(27)
		case '0':
			b.WriteByte(0)
		default:
			b.WriteByte('\\')
			b.WriteByte(s[i])
		}
	}
	return b.String()
}
