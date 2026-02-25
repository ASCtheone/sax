//go:build !windows

package updater

import "os"

// replaceBinary atomically replaces the executable using rename.
func replaceBinary(exe, tmpFile string) error {
	if err := os.Chmod(tmpFile, 0755); err != nil {
		os.Remove(tmpFile)
		return err
	}
	return os.Rename(tmpFile, exe)
}
