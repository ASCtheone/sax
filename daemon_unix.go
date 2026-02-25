//go:build !windows

package main

import (
	"os/exec"
	"syscall"
)

func setUnixProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid: true,
	}
}

func setWindowsProcAttr(_ *exec.Cmd) {
	// no-op on Unix
}
