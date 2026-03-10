package mcp

import (
	"os/exec"
	"syscall"
)

// isProcessAlive checks if a process with the given PID is running on Windows.
func isProcessAlive(pid int) bool {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	openProcess := kernel32.NewProc("OpenProcess")
	const processQueryLimitedInfo = 0x1000
	h, _, _ := openProcess.Call(processQueryLimitedInfo, 0, uintptr(pid))
	if h == 0 {
		return false
	}
	syscall.CloseHandle(syscall.Handle(h))
	return true
}

// setLaunchProcAttr sets Windows-specific process attributes for launched apps.
func setLaunchProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: 0x00000010, // CREATE_NEW_CONSOLE
	}
}
