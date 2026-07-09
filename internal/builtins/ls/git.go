package ls

import (
	"os/exec"
	"path/filepath"
	"strings"
)

// gitRepo holds the porcelain status of a working tree, keyed by repo-root
// relative slash paths.
type gitRepo struct {
	root   string
	status map[string][2]byte
}

// loadGitStatus returns the status for the repository containing dir, or nil if
// dir is not inside a git work tree or git is unavailable.
func loadGitStatus(dir string) *gitRepo {
	root, err := gitRoot(dir)
	if err != nil {
		return nil
	}
	out, err := exec.Command("git", "-C", dir,
		"status", "--porcelain", "-z", "--untracked-files=all").Output()
	if err != nil {
		return nil
	}
	return &gitRepo{root: root, status: parsePorcelain(out)}
}

func gitRoot(dir string) (string, error) {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// parsePorcelain parses `git status --porcelain -z` output into path -> XY code.
// Each NUL-separated record is "XY PATH"; rename/copy records are followed by an
// extra NUL field (the original path) which we consume.
func parsePorcelain(data []byte) map[string][2]byte {
	res := map[string][2]byte{}
	parts := strings.Split(string(data), "\x00")
	for i := 0; i < len(parts); i++ {
		rec := parts[i]
		if len(rec) < 4 {
			continue
		}
		x, y, path := rec[0], rec[1], rec[3:]
		if x == 'R' || x == 'C' {
			i++ // skip the original-path field that trails a rename/copy
		}
		res[filepath.ToSlash(path)] = [2]byte{x, y}
	}
	return res
}

// annotateGit attaches each entry's status. Directories aggregate: a directory
// is marked if any tracked change exists beneath it.
func annotateGit(repo *gitRepo, ents []entry) {
	for i := range ents {
		e := &ents[i]
		rel, err := filepath.Rel(repo.root, e.path)
		if err != nil {
			continue
		}
		rel = filepath.ToSlash(rel)
		if code, ok := repo.status[rel]; ok {
			e.gitX, e.gitY, e.gitSet = code[0], code[1], true
			continue
		}
		if e.info.IsDir() {
			prefix := rel + "/"
			for p, code := range repo.status {
				if strings.HasPrefix(p, prefix) {
					e.gitX, e.gitY, e.gitSet = code[0], code[1], true
					break
				}
			}
		}
	}
}

// gitCell maps a porcelain status byte to a display rune and its color.
func gitCell(code byte) (byte, string) {
	switch code {
	case 'M':
		return 'M', cYellow
	case 'A':
		return 'A', cGreen
	case '?':
		return 'N', cGreen // new / untracked
	case 'D':
		return 'D', cRed
	case 'R':
		return 'R', cCyan
	case 'C':
		return 'C', cCyan
	case 'U':
		return 'U', cBrightRed // unmerged / conflict
	case '!':
		return 'I', cGray // ignored
	default: // ' ' or 0 == clean
		return '-', cGray
	}
}

// gitPlain is the uncolored two-cell status (staged, unstaged) for width math.
func gitPlain(e entry) string {
	if !e.gitSet {
		return "--"
	}
	x, _ := gitCell(e.gitX)
	y, _ := gitCell(e.gitY)
	return string([]byte{x, y})
}

// gitColored is the two-cell status with per-cell color.
func gitColored(e entry) string {
	if !e.gitSet {
		return cGray + "--" + cReset
	}
	x, cx := gitCell(e.gitX)
	y, cy := gitCell(e.gitY)
	return cx + string(x) + cReset + cy + string(y) + cReset
}
