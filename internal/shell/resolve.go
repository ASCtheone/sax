package shell

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// resolveCommand finds the executable to run for a typed command name, so that
// programs can be launched without an extension, a ./ prefix, or `start`:
//
//   - A name containing a path separator (or absolute) is resolved against the
//     shell's working directory, trying executable extensions.
//   - A bare name is looked up in the current directory first (this is what lets
//     `foo` run `./foo` without the prefix), then on PATH.
//
// It returns the resolved path and true, or false when nothing runnable matches.
// Resolution uses the shell's cwd (s.cwd), not the process cwd, since ss tracks
// its own working directory.
func (s *Shell) resolveCommand(name string) (string, bool) {
	if name == "" {
		return "", false
	}

	if filepath.IsAbs(name) || strings.ContainsAny(name, `/\`) {
		base := name
		if !filepath.IsAbs(base) {
			base = filepath.Join(s.cwd, name)
		}
		return resolveWithExt(base)
	}

	// Bare name: current directory first, then PATH.
	if p, ok := resolveWithExt(filepath.Join(s.cwd, name)); ok {
		return p, true
	}
	if p, err := exec.LookPath(name); err == nil {
		return p, true
	}
	return "", false
}

// spawnCommand returns the program and arguments to actually launch for a
// resolved executable path. On Windows, .bat/.cmd files cannot be started
// directly by CreateProcess (used by both os/exec and the PTY layer), so they
// are run through cmd.exe /c.
func spawnCommand(path string, args []string) (string, []string) {
	if runtime.GOOS == "windows" {
		switch strings.ToLower(filepath.Ext(path)) {
		case ".bat", ".cmd":
			return "cmd.exe", append([]string{"/c", path}, args...)
		}
	}
	return path, args
}

// resolveWithExt returns path if it is a runnable file, otherwise (on Windows)
// path with each PATHEXT extension appended.
func resolveWithExt(path string) (string, bool) {
	if isExecutableFile(path) {
		return path, true
	}
	if runtime.GOOS == "windows" {
		for _, ext := range pathExts() {
			if cand := path + ext; isExecutableFile(cand) {
				return cand, true
			}
		}
	}
	return "", false
}

// isExecutableFile reports whether path is an existing, runnable file. On
// Windows "runnable" means its extension is in PATHEXT; elsewhere it means an
// execute bit is set.
func isExecutableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	if runtime.GOOS != "windows" {
		return info.Mode()&0o111 != 0
	}
	ext := strings.ToLower(filepath.Ext(path))
	if ext == "" {
		return false
	}
	for _, e := range pathExts() {
		if e == ext {
			return true
		}
	}
	return false
}

// pathExts returns the lowercased executable extensions from %PATHEXT%, with a
// sensible default.
func pathExts() []string {
	v := os.Getenv("PATHEXT")
	if v == "" {
		v = ".COM;.EXE;.BAT;.CMD"
	}
	parts := strings.Split(v, ";")
	exts := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			exts = append(exts, strings.ToLower(p))
		}
	}
	return exts
}
