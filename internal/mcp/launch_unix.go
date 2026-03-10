//go:build !windows

package mcp

import (
	"os"
	"os/exec"
	"syscall"
)

// isProcessAlive checks if a process with the given PID is running on Unix.
func isProcessAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

// setLaunchProcAttr sets Unix-specific process attributes for launched apps.
func setLaunchProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true,
	}
}
