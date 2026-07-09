// Package prompt renders the ss prompt. The default style mirrors the popular
// oh-my-zsh "robbyrussell" theme: a colored arrow that turns red on a failed
// command, the current directory, and git branch/dirty status.
package prompt

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	reset    = "\x1b[0m"
	green    = "\x1b[32m"
	red      = "\x1b[31m"
	blue     = "\x1b[34m"
	yellow   = "\x1b[33m"
	boldCyan = "\x1b[1;36m"

	arrow = "➜" // ➜
	cross = "✗" // ✗
)

// Render builds the prompt for the given working directory and last exit code.
// When color is false, ANSI codes are omitted (batch / non-terminal use).
func Render(cwd string, lastErr int, color bool) string {
	dir := shortDir(cwd)
	branch, dirty, inRepo := gitInfo(cwd)

	if !color {
		s := arrow + "  " + dir
		if inRepo {
			s += " git:(" + branch + ")"
			if dirty {
				s += " " + cross
			}
		}
		return s + " "
	}

	arrowColor := green
	if lastErr != 0 {
		arrowColor = red
	}

	var b strings.Builder
	b.WriteString(arrowColor + arrow + reset + "  ")
	b.WriteString(boldCyan + dir + reset)
	if inRepo {
		b.WriteString(" " + blue + "git:(" + reset + red + branch + reset + blue + ")" + reset)
		if dirty {
			b.WriteString(" " + yellow + cross + reset)
		}
	}
	b.WriteString(" ")
	return b.String()
}

// shortDir returns the trailing path component (like zsh %c), collapsing the
// home directory to ~.
func shortDir(cwd string) string {
	if home, err := os.UserHomeDir(); err == nil && cwd == home {
		return "~"
	}
	base := filepath.Base(cwd)
	if base == "" || base == string(filepath.Separator) || base == "." {
		return cwd
	}
	return base
}

// gitInfo returns the current branch and whether the work tree is dirty, plus
// whether dir is inside a git repository at all. Failures (no git, not a repo)
// return inRepo=false.
func gitInfo(dir string) (branch string, dirty, inRepo bool) {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--abbrev-ref", "HEAD").Output()
	if err != nil {
		return "", false, false
	}
	branch = strings.TrimSpace(string(out))
	if branch == "" {
		return "", false, false
	}
	if st, err := exec.Command("git", "-C", dir, "status", "--porcelain").Output(); err == nil {
		dirty = len(strings.TrimSpace(string(st))) > 0
	}
	return branch, dirty, true
}
