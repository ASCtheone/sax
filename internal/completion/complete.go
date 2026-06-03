// Package completion provides shell-completion candidate generation for sax and
// nx, plus the shell scripts that wire them up. The design mirrors the
// cobra/kubectl pattern: shells call a hidden "__complete" command and sax
// returns newline-separated candidates.
//
// sax always filters candidates by the prefix of the last word, so every shell
// receives an already-narrowed list regardless of how it filters afterward.
package completion

import (
	"sort"
	"strings"

	"github.com/asc/sax/internal/config"
	"github.com/asc/sax/internal/ipc"
	"github.com/asc/sax/internal/nx"
)

// EmptyWordSentinel stands in for an empty trailing word being completed.
// Windows PowerShell 5.1 silently drops empty-string arguments to native
// executables, so the pwsh completion script sends this token instead and the
// candidate functions convert it back to "". bash/zsh pass empty words
// directly and never send it.
const EmptyWordSentinel = "__SAX_EMPTY__"

// normalizeWords converts a trailing EmptyWordSentinel back into an empty
// string so the partial-word logic works uniformly across shells.
func normalizeWords(words []string) []string {
	n := len(words)
	if n > 0 && words[n-1] == EmptyWordSentinel {
		out := append([]string(nil), words...)
		out[n-1] = ""
		return out
	}
	return words
}

// topLevelCommands are sax's own commands and flags offered at the first
// argument position.
func topLevelCommands() []string {
	return []string{
		"nx", "mcp", "themes", "setup", "completion", "update",
		"-a", "-c", "-ca", "-x", "-xa", "-l", "--list",
		"--kill", "--kill-all", "--tail", "--send", "--status", "--version",
	}
}

// nxSubcommands is a static set of nx verbs plus sax-managed verbs, used to
// complete the first nx argument.
func nxSubcommands() []string {
	return []string{
		// Common nx commands
		"build", "test", "lint", "e2e", "serve", "dev", "start", "run",
		"run-many", "affected", "graph", "show", "generate", "g", "list",
		"report", "migrate", "release", "init", "add", "watch", "sync",
		"reset", "daemon", "format", "connect", "login", "logout", "repair",
		"exec", "preview", "deploy",
		// sax-managed verbs
		"stop",
	}
}

// SaxCandidates returns completion candidates for the `sax` command. words are
// the arguments after "sax", where the last element is the (possibly empty)
// word being completed.
func SaxCandidates(words []string) []string {
	partial, ctx := splitPartial(normalizeWords(words))

	if len(ctx) == 0 {
		return filterPrefix(topLevelCommands(), partial)
	}

	switch ctx[0] {
	case "nx":
		// Delegate to nx completion for everything after "nx".
		return NxCandidates(append(append([]string{}, ctx[1:]...), partial))
	case "-a", "attach", "--kill", "--tail", "--send", "--status":
		if len(ctx) == 1 {
			return filterPrefix(sessionNames(), partial)
		}
	case "themes", "theme":
		if len(ctx) == 1 {
			return filterPrefix([]string{"set"}, partial)
		}
		if ctx[1] == "set" {
			return filterPrefix(config.PresetNames(), partial)
		}
	case "completion":
		if len(ctx) == 1 {
			return filterPrefix([]string{"zsh", "bash", "pwsh"}, partial)
		}
	case "setup":
		if len(ctx) == 1 {
			return filterPrefix([]string{"completion"}, partial)
		}
	}
	return nil
}

// NxCandidates returns completion candidates for the `nx` command. words are the
// arguments after "nx", where the last element is the word being completed.
// Project and target names come from sax's own nx workspace discovery, so no
// nx process is spawned per keystroke.
func NxCandidates(words []string) []string {
	partial, ctx := splitPartial(normalizeWords(words))

	if len(ctx) == 0 {
		return filterPrefix(nxSubcommands(), partial)
	}

	switch ctx[0] {
	case "run":
		return filterPrefix(nxRunTargets(partial), partial)
	case "build", "test", "lint", "e2e", "serve", "dev", "start", "watch",
		"deploy", "preview", "export", "stop":
		return filterPrefix(nxProjects(), partial)
	case "show":
		if len(ctx) == 1 {
			return filterPrefix([]string{"projects", "project"}, partial)
		}
		if ctx[1] == "project" {
			return filterPrefix(nxProjects(), partial)
		}
	}
	return filterPrefix(nxProjects(), partial)
}

// splitPartial separates the trailing word being completed from the preceding
// context. An empty words slice yields an empty partial and context.
func splitPartial(words []string) (partial string, ctx []string) {
	if len(words) == 0 {
		return "", nil
	}
	return words[len(words)-1], words[:len(words)-1]
}

// filterPrefix returns the candidates that start with partial, sorted and
// de-duplicated.
func filterPrefix(cands []string, partial string) []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range cands {
		if c == "" || seen[c] {
			continue
		}
		if strings.HasPrefix(c, partial) {
			seen[c] = true
			out = append(out, c)
		}
	}
	sort.Strings(out)
	return out
}

// sessionNames returns the names of currently alive sax sessions.
func sessionNames() []string {
	sessions, err := ipc.ListSessions()
	if err != nil {
		return nil
	}
	var names []string
	for _, s := range sessions {
		if ipc.IsSessionAlive(s.Name) {
			names = append(names, s.Name)
		}
	}
	return names
}

// nxProjects returns the names of projects in the nx workspace containing the
// current directory, or nil if there is none.
func nxProjects() []string {
	ws, err := nx.Discover("")
	if err != nil {
		return nil
	}
	var names []string
	for _, p := range ws.Projects {
		names = append(names, p.Name)
	}
	return names
}

// nxRunTargets completes `nx run` arguments. With no ':' in the partial it
// offers "project:" prefixes; once a project is chosen (partial contains ':')
// it offers that project's "project:target" combinations.
func nxRunTargets(partial string) []string {
	ws, err := nx.Discover("")
	if err != nil {
		return nil
	}
	if i := strings.Index(partial, ":"); i >= 0 {
		projName := partial[:i]
		for _, p := range ws.Projects {
			if p.Name == projName {
				var out []string
				for _, t := range p.Targets {
					out = append(out, p.Name+":"+t.Name)
				}
				return out
			}
		}
		return nil
	}
	var out []string
	for _, p := range ws.Projects {
		out = append(out, p.Name+":")
	}
	return out
}
