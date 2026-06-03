package pty

import (
	"os"
	"os/exec"
	"runtime"
)

// shellOverride and extraEnv are daemon-wide spawn defaults sourced from
// ~/.saxrc. They are set once via Configure at server startup, before any
// session is created, and treated as read-only thereafter.
var (
	shellOverride string
	extraEnv      []string
)

// Configure sets daemon-wide spawn defaults from .saxrc. shell is a shell name
// or path ("set shell"); env is a list of "KEY=VALUE" strings injected into
// every pane. Call once at startup, before the first session is created.
func Configure(shell string, env []string) {
	shellOverride = shell
	extraEnv = append([]string(nil), env...)
}

// DetectShell returns the default shell for the current platform, honoring a
// "set shell" override from .saxrc when present and resolvable.
func DetectShell() (string, []string) {
	if shellOverride != "" {
		if path, err := exec.LookPath(shellOverride); err == nil {
			return path, []string{shellOverride}
		}
		// Fall through to platform detection if the override can't be found.
	}
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
