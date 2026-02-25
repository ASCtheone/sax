package pty

import (
	"os"
	"os/exec"
	"runtime"
)

// DetectShell returns the default shell for the current platform.
func DetectShell() (string, []string) {
	switch runtime.GOOS {
	case "windows":
		return detectWindowsShell()
	default:
		return detectUnixShell()
	}
}

func detectUnixShell() (string, []string) {
	if shell := os.Getenv("SHELL"); shell != "" {
		return shell, []string{shell}
	}
	for _, sh := range []string{"zsh", "bash", "sh"} {
		if p, err := exec.LookPath(sh); err == nil {
			return p, []string{sh}
		}
	}
	return "/bin/sh", []string{"sh"}
}

func detectWindowsShell() (string, []string) {
	// Prefer PowerShell 7+, then Windows PowerShell, then cmd.exe
	if p, err := exec.LookPath("pwsh.exe"); err == nil {
		return p, []string{"pwsh.exe", "-NoLogo"}
	}
	if p, err := exec.LookPath("powershell.exe"); err == nil {
		return p, []string{"powershell.exe", "-NoLogo"}
	}
	if comspec := os.Getenv("COMSPEC"); comspec != "" {
		return comspec, []string{comspec}
	}
	return "cmd.exe", []string{"cmd.exe"}
}
