//go:build windows

package ls

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// isHidden treats both dotfiles (Unix convention) and files carrying the
// Windows hidden attribute as hidden.
func isHidden(name string, info os.FileInfo) bool {
	if name != "." && name != ".." && strings.HasPrefix(name, ".") {
		return true
	}
	if d, ok := info.Sys().(*syscall.Win32FileAttributeData); ok {
		return d.FileAttributes&syscall.FILE_ATTRIBUTE_HIDDEN != 0
	}
	return false
}

// winExecExt lists extensions Windows treats as directly executable.
var winExecExt = map[string]bool{
	".exe": true, ".bat": true, ".cmd": true, ".com": true, ".ps1": true,
}

// isExecutable reports whether an entry is runnable, by extension on Windows.
func isExecutable(e entry) bool {
	if e.info.IsDir() {
		return false
	}
	return winExecExt[strings.ToLower(filepath.Ext(e.name))]
}
