// Package builtins defines the contract for ss builtin commands. Each builtin
// lives in its own package (one feature per package) and is wired into the
// shell through a Registry, keeping the shell core decoupled from individual
// features.
package builtins

import "io"

// Env is everything a builtin needs to run: its arguments, the working
// directory, output streams, and presentation hints about the terminal.
type Env struct {
	Cwd    string    // absolute current working directory
	Args   []string  // arguments following the command name
	Stdout io.Writer // normal output
	Stderr io.Writer // diagnostics
	Width  int       // terminal width in columns; 0 when output is not a terminal
	Color  bool      // true when ANSI color output is appropriate
}

// Command is a self-contained builtin. Run returns a Unix-style exit code
// (0 success, non-zero failure).
type Command interface {
	Name() string
	Run(env *Env) int
}

// Registry resolves command names to their implementations.
type Registry struct {
	cmds map[string]Command
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{cmds: make(map[string]Command)}
}

// Register adds a command, replacing any existing one with the same name.
func (r *Registry) Register(c Command) {
	r.cmds[c.Name()] = c
}

// Lookup returns the command registered under name, if any.
func (r *Registry) Lookup(name string) (Command, bool) {
	c, ok := r.cmds[name]
	return c, ok
}
