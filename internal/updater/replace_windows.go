//go:build windows

package updater

import "os"

// replaceBinary replaces the running exe on Windows by renaming around the lock.
// Windows locks the running exe, so: exe→exe.old, tmp→exe, remove .old.
func replaceBinary(exe, tmpFile string) error {
	oldFile := exe + ".old"

	// Remove any leftover .old from a previous update
	os.Remove(oldFile)

	// Move running exe out of the way
	if err := os.Rename(exe, oldFile); err != nil {
		os.Remove(tmpFile)
		return err
	}

	// Move new binary into place
	if err := os.Rename(tmpFile, exe); err != nil {
		// Try to restore the original
		os.Rename(oldFile, exe)
		os.Remove(tmpFile)
		return err
	}

	// Best-effort cleanup of .old (may fail if still locked)
	os.Remove(oldFile)
	return nil
}
