//go:build windows

package vnet

import (
	"os"

	"golang.org/x/sys/windows"
)

// lockRange covers the whole file; the lock file is never written.
const lockRange = 1

func lockFileExclusive(f *os.File) error {
	ol := new(windows.Overlapped)
	return windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, lockRange, 0, ol)
}

func unlockFile(f *os.File) {
	ol := new(windows.Overlapped)
	_ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, lockRange, 0, ol)
}
