// Package ls implements the `ls` builtin: a cross-platform directory lister
// with column and long output, sorting, hidden-file handling, type coloring,
// and classifiers. It is self-contained — the only sax dependency is the
// builtins contract.
package ls

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/asc/sax/internal/builtins"
)

// Command is the ls builtin.
type Command struct{}

// New returns a ready-to-register ls command.
func New() *Command { return &Command{} }

// Name implements builtins.Command.
func (c *Command) Name() string { return "ls" }

// options holds the parsed flags for one invocation.
type options struct {
	all       bool // -a: include entries starting with . plus . and ..
	almostAll bool // -A: like -a but without . and ..
	long      bool // -l: long format
	one       bool // -1: one entry per line
	reverse   bool // -r: reverse sort order
	byTime    bool // -t: sort by modification time, newest first
	bySize    bool // -S: sort by size, largest first
	human     bool // -h: human-readable sizes (with -l)
	classify  bool // -F: append indicator (*/=>@|) to entries
	dirOnly   bool // -d: list directories themselves, not their contents
	mixDirs   bool // --no-group-directories-first: don't float dirs to the top
	git       bool // --git: show a git status column
	noGit     bool // --no-git: never show the git status column
	gitCol    bool // computed: whether to show the git column for this run
}

// entry is a single thing to display.
type entry struct {
	name string
	info os.FileInfo
	path string // absolute path, for symlink and attribute lookups

	gitX, gitY byte // porcelain status (staged, unstaged)
	gitSet     bool // true when a git status applies to this entry
}

// Run parses arguments and lists the requested paths.
func (c *Command) Run(env *builtins.Env) int {
	opts, paths, err := parseArgs(env.Args)
	if err != nil {
		fmt.Fprintf(env.Stderr, "ls: %v\n", err)
		return 2
	}
	if len(paths) == 0 {
		paths = []string{"."}
	}
	return c.list(env, opts, paths)
}

// list resolves operands, separating files from directories, then renders them.
func (c *Command) list(env *builtins.Env, o options, paths []string) int {
	type operand struct {
		arg  string
		abs  string
		info os.FileInfo
	}
	var files, dirs []operand
	code := 0

	// The git column is shown when requested (or implied by -l) and suppressed
	// outside a repo (handled per-listing in emit).
	o.gitCol = (o.git || o.long) && !o.noGit

	for _, p := range paths {
		abs := p
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(env.Cwd, abs)
		}
		info, err := os.Lstat(abs)
		if err != nil {
			fmt.Fprintf(env.Stderr, "ls: cannot access '%s': %s\n", p, notFound(err))
			code = 2
			continue
		}
		op := operand{arg: p, abs: abs, info: info}
		if info.IsDir() && !o.dirOnly {
			dirs = append(dirs, op)
		} else {
			files = append(files, op)
		}
	}

	// Non-directory operands are listed first, as a single group.
	if len(files) > 0 {
		ents := make([]entry, 0, len(files))
		for _, f := range files {
			ents = append(ents, entry{name: f.arg, info: f.info, path: f.abs})
		}
		c.emit(env, o, ents, env.Cwd)
	}

	multiple := len(files)+len(dirs) > 1
	for i, d := range dirs {
		if len(files) > 0 || i > 0 {
			fmt.Fprintln(env.Stdout)
		}
		if multiple {
			fmt.Fprintf(env.Stdout, "%s:\n", d.arg)
		}
		ents, err := readDir(d.abs, o)
		if err != nil {
			fmt.Fprintf(env.Stderr, "ls: cannot open directory '%s': %s\n", d.arg, notFound(err))
			code = 2
			continue
		}
		c.emit(env, o, ents, d.abs)
	}
	return code
}

// emit annotates git status (when enabled and in a repo), sorts, and renders one
// group of entries. o is taken by value so disabling the git column here does
// not leak to other listings.
func (c *Command) emit(env *builtins.Env, o options, ents []entry, dir string) {
	if o.gitCol {
		if repo := loadGitStatus(dir); repo != nil {
			annotateGit(repo, ents)
		} else {
			o.gitCol = false // not a repo: don't show a misleading column
		}
	}
	sortEntries(ents, o)
	c.render(env, o, ents)
}

// readDir collects the entries of a directory, honoring hidden-file options and
// synthesizing . and .. for -a.
func readDir(dir string, o options) ([]entry, error) {
	des, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var ents []entry
	if o.all {
		if info, err := os.Lstat(dir); err == nil {
			ents = append(ents, entry{name: ".", info: info, path: dir})
		}
		parent := filepath.Dir(dir)
		if info, err := os.Lstat(parent); err == nil {
			ents = append(ents, entry{name: "..", info: info, path: parent})
		}
	}

	for _, de := range des {
		name := de.Name()
		info, err := de.Info()
		if err != nil {
			continue
		}
		if !o.all && !o.almostAll && isHidden(name, info) {
			continue
		}
		ents = append(ents, entry{name: name, info: info, path: filepath.Join(dir, name)})
	}
	return ents, nil
}

// sortEntries orders entries per the active options (name by default).
func sortEntries(ents []entry, o options) {
	less := func(i, j int) bool {
		a, b := ents[i], ents[j]
		if o.byTime && !a.info.ModTime().Equal(b.info.ModTime()) {
			return a.info.ModTime().After(b.info.ModTime()) // newest first
		}
		if o.bySize && a.info.Size() != b.info.Size() {
			return a.info.Size() > b.info.Size() // largest first
		}
		return naturalLess(a.name, b.name)
	}
	sort.SliceStable(ents, less)
	if o.reverse {
		for i, j := 0, len(ents)-1; i < j; i, j = i+1, j-1 {
			ents[i], ents[j] = ents[j], ents[i]
		}
	}
	if !o.mixDirs {
		groupDirsFirst(ents)
	}
}

// groupDirsFirst stably floats directories to the top, preserving the existing
// order within each group. Modern listers do this by default; disable with
// --no-group-directories-first.
func groupDirsFirst(ents []entry) {
	sort.SliceStable(ents, func(i, j int) bool {
		di, dj := ents[i].info.IsDir(), ents[j].info.IsDir()
		return di && !dj
	})
}

// parseArgs splits args into options and path operands. Flag parsing stops at
// "--"; "-" alone is treated as a path.
func parseArgs(args []string) (options, []string, error) {
	var o options
	var paths []string
	flagsDone := false

	for _, a := range args {
		switch {
		case flagsDone, a == "-", !strings.HasPrefix(a, "-"):
			paths = append(paths, a)
		case a == "--":
			flagsDone = true
		case strings.HasPrefix(a, "--"):
			if err := o.setLong(a[2:]); err != nil {
				return o, nil, err
			}
		default:
			for _, r := range a[1:] {
				if err := o.setShort(r); err != nil {
					return o, nil, err
				}
			}
		}
	}
	return o, paths, nil
}

func (o *options) setShort(r rune) error {
	switch r {
	case 'a':
		o.all = true
	case 'A':
		o.almostAll = true
	case 'l':
		o.long = true
	case '1':
		o.one = true
	case 'r':
		o.reverse = true
	case 't':
		o.byTime = true
	case 'S':
		o.bySize = true
	case 'h':
		o.human = true
	case 'F':
		o.classify = true
	case 'd':
		o.dirOnly = true
	default:
		return fmt.Errorf("invalid option -- '%c'", r)
	}
	return nil
}

func (o *options) setLong(name string) error {
	switch name {
	case "all":
		o.all = true
	case "almost-all":
		o.almostAll = true
	case "long":
		o.long = true
	case "reverse":
		o.reverse = true
	case "human-readable":
		o.human = true
	case "classify":
		o.classify = true
	case "directory":
		o.dirOnly = true
	case "group-directories-first":
		o.mixDirs = false
	case "no-group-directories-first":
		o.mixDirs = true
	case "git":
		o.git = true
	case "no-git":
		o.noGit = true
	default:
		return fmt.Errorf("unrecognized option '--%s'", name)
	}
	return nil
}

// notFound renders a friendlier message for the common os errors.
func notFound(err error) string {
	if os.IsNotExist(err) {
		return "No such file or directory"
	}
	if os.IsPermission(err) {
		return "Permission denied"
	}
	return err.Error()
}
