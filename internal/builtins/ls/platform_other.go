//go:build !windows

package ls

import (
	"os"
	"strings"
)

// isHidden follows the Unix convention: names beginning with a dot are hidden.
func isHidden(name string, info os.FileInfo) bool {
	return name != "." && name != ".." && strings.HasPrefix(name, ".")
}

// isExecutable reports whether an entry has any execute bit set.
func isExecutable(e entry) bool {
	return !e.info.IsDir() && e.info.Mode()&0o111 != 0
}
