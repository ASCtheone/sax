// Package saxrc parses the ~/.saxrc configuration file: a small, zsh-flavored
// line-based DSL for configuring sax. The result is an immutable Config that
// the daemon and client consult at startup.
//
// Supported directives:
//
//	set <key> <value>         options (shell, prefix, theme, history-limit, ...)
//	setenv KEY value          environment variable for new panes
//	export KEY=value          environment variable for new panes
//	alias name="command"      command-mode alias
//	function name { a; b }     command macro (sequence of sax commands)
//	bind <key> <command>      bind a key (in prefix mode) to a sax command
//	unbind <key>              remove a default binding
//	hook <event> "command"    lifecycle hook (new-pane, session-start, ...)
//	run "<command>"           run a sax command at session startup
//	source <path>             include another .saxrc file
//
// Lines beginning with '#' (after leading whitespace) are comments. A trailing
// '\' continues a logical line onto the next physical line.
package saxrc

import "strconv"

// Bind maps a key (normalized to the string form bubbletea reports, e.g.
// "ctrl+a", "r", "|") to a sax command string such as "split-v" or
// "select-tab 1".
type Bind struct {
	Key     string
	Command string
}

// EnvVar is an environment variable injected into every spawned pane.
type EnvVar struct {
	Key   string
	Value string
}

// Config is the parsed, immutable result of loading a .saxrc file. Callers must
// treat it as read-only; the parser is the only writer.
type Config struct {
	Options  map[string]string   // set <key> <value>
	Env      []EnvVar            // setenv / export, in file order
	Aliases  map[string]string   // alias name -> command
	Funcs    map[string][]string // function name -> command sequence
	Binds    []Bind              // bind directives, in file order
	Unbinds  []string            // unbind directives (normalized keys)
	Hooks    map[string][]string // event -> commands, in file order
	RunCmds  []string            // run directives, in file order
	Warnings []string            // non-fatal parse problems (line-prefixed)
	Path     string              // resolved file that produced this config ("" if none)
}

// newConfig returns an empty, fully-initialized Config.
func newConfig() *Config {
	return &Config{
		Options: map[string]string{},
		Aliases: map[string]string{},
		Funcs:   map[string][]string{},
		Hooks:   map[string][]string{},
	}
}

// Option returns the raw string value of a set option and whether it was set.
func (c *Config) Option(key string) (string, bool) {
	if c == nil {
		return "", false
	}
	v, ok := c.Options[key]
	return v, ok
}

// Shell returns the configured default shell ("set shell"), or "" if unset.
func (c *Config) Shell() string {
	v, _ := c.Option("shell")
	return v
}

// Prefix returns the configured prefix key normalized to a tea key string
// (e.g. "ctrl+a"), or "" if unset.
func (c *Config) Prefix() string {
	v, ok := c.Option("prefix")
	if !ok {
		return ""
	}
	return NormalizeKey(v)
}

// ThemeName returns the configured theme preset name ("set theme"), or "".
func (c *Config) ThemeName() string {
	v, _ := c.Option("theme")
	return v
}

// DefaultDir returns the configured default working directory for new
// sessions/panes ("set default-dir"), or "".
func (c *Config) DefaultDir() string {
	v, _ := c.Option("default-dir")
	return v
}

// ThemeOverrides returns the inline theme color overrides ("set theme.accent
// #ff0000") keyed by the field name after the dot (e.g. "accent").
func (c *Config) ThemeOverrides() map[string]string {
	if c == nil {
		return nil
	}
	out := map[string]string{}
	for k, v := range c.Options {
		if len(k) > 6 && k[:6] == "theme." {
			out[k[6:]] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// HistoryLimit returns the configured scrollback line limit and whether it was
// set to a valid positive integer.
func (c *Config) HistoryLimit() (int, bool) {
	v, ok := c.Option("history-limit")
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// BoolOption interprets an option as a boolean. Recognized true values:
// on/true/yes/1; false values: off/false/no/0. The second return is whether
// the option was set to a recognized value.
func (c *Config) BoolOption(key string) (bool, bool) {
	v, ok := c.Option(key)
	if !ok {
		return false, false
	}
	switch v {
	case "on", "true", "yes", "1":
		return true, true
	case "off", "false", "no", "0":
		return false, true
	default:
		return false, false
	}
}

// EnvStrings returns the environment variables as "KEY=VALUE" pairs suitable
// for appending to a process environment.
func (c *Config) EnvStrings() []string {
	if c == nil {
		return nil
	}
	out := make([]string, 0, len(c.Env))
	for _, e := range c.Env {
		out = append(out, e.Key+"="+e.Value)
	}
	return out
}
