package vnet

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// The sax daemon is a Go program without the shim, so it applies the same
// two rewrites as the shim's exec hooks (shim/src/exec.c) to the first command
// it starts in a macOS session; after that, the shim takes over.

var (
	restrictedDirs = []string{"/bin/", "/sbin/", "/usr/bin/", "/usr/sbin/", "/usr/libexec/", "/System/"}
	posixShells    = map[string]bool{"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true}
)

const envShebang = "#!/usr/bin/env "

func isRestricted(path string) bool {
	for _, d := range restrictedDirs {
		if strings.HasPrefix(path, d) {
			return true
		}
	}
	return false
}

// LaunderCommand rewrites argv so the shim survives macOS SIP binaries:
//
//	sh -c "script"            -> sh -c "export DYLD_INSERT_LIBRARIES=..\nscript"
//	tool (#!/usr/bin/env X)   -> X tool args   (skips the SIP-protected env)
//
// Anything else, any command outside a router and every command on Linux is
// returned unchanged. lookPath resolves command names (exec.LookPath).
func LaunderCommand(argv []string, getenv func(string) string, lookPath func(string) (string, error)) []string {
	lib := getenv(EnvShim)
	if len(argv) == 0 || lib == "" || !sipWorkarounds() {
		return argv
	}
	path, err := lookPath(argv[0])
	if err != nil {
		return argv
	}
	if isRestricted(path) && posixShells[filepath.Base(path)] {
		return prefixShellScript(argv, lib)
	}
	return skipEnvShebang(argv, path, lookPath)
}

func prefixShellScript(argv []string, lib string) []string {
	for i := 1; i+1 < len(argv); i++ {
		a := argv[i]
		if !strings.HasPrefix(a, "-") {
			return argv // reached operands: no -c script
		}
		if strings.HasPrefix(a, "--") || !strings.Contains(a, "c") {
			continue
		}
		out := append([]string{}, argv...)
		out[i+1] = exportLine(lib) + "\n" + argv[i+1]
		return out
	}
	return argv
}

func skipEnvShebang(argv []string, path string, lookPath func(string) (string, error)) []string {
	f, err := os.Open(path)
	if err != nil {
		return argv
	}
	line, _ := bufio.NewReader(f).ReadString('\n')
	f.Close()
	rest, ok := strings.CutPrefix(strings.TrimSpace(line), envShebang)
	if !ok {
		return argv
	}
	fields := strings.Fields(rest)
	if len(fields) == 0 || strings.HasPrefix(fields[0], "-") || strings.Contains(fields[0], "=") {
		return argv
	}
	interp, err := lookPath(fields[0])
	if err != nil || isRestricted(interp) {
		return argv
	}
	out := []string{interp}
	if len(fields) > 1 {
		out = append(out, fields[1]) // one shebang argument, like the kernel
	}
	return append(append(out, path), argv[1:]...)
}
