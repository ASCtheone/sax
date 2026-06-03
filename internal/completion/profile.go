package completion

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	blockStart = "# >>> sax completion >>>"
	blockEnd   = "# <<< sax completion <<<"
)

// initLine returns the single line added to a shell profile to load sax
// completion at startup. It evaluates `sax completion <shell>` live, so the
// completion logic always matches the installed sax binary.
func initLine(shell string) (string, error) {
	switch strings.ToLower(shell) {
	case "zsh":
		return "command -v sax >/dev/null 2>&1 && source <(sax completion zsh)", nil
	case "bash":
		return "command -v sax >/dev/null 2>&1 && source <(sax completion bash)", nil
	case "pwsh", "powershell":
		return "if (Get-Command sax -ErrorAction SilentlyContinue) { sax completion pwsh | Out-String | Invoke-Expression }", nil
	default:
		return "", fmt.Errorf("unsupported shell %q (use zsh, bash, or pwsh)", shell)
	}
}

// ProfilePath returns the shell profile/rc file completion should be installed
// into for the given shell.
func ProfilePath(shell string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	switch strings.ToLower(shell) {
	case "zsh":
		return filepath.Join(home, ".zshrc"), nil
	case "bash":
		return filepath.Join(home, ".bashrc"), nil
	case "pwsh", "powershell":
		return pwshProfilePath(shell, home), nil
	default:
		return "", fmt.Errorf("unsupported shell %q (use zsh, bash, or pwsh)", shell)
	}
}

// pwshProfilePath asks PowerShell for its real $PROFILE (which can live under a
// relocated/OneDrive Documents folder), falling back to the conventional path.
func pwshProfilePath(shell, home string) string {
	bin := "pwsh"
	if strings.EqualFold(shell, "powershell") {
		bin = "powershell"
	}
	if p, err := exec.LookPath(bin); err == nil {
		out, err := exec.Command(p, "-NoProfile", "-NonInteractive", "-Command",
			"$PROFILE.CurrentUserAllHosts").Output()
		if path := strings.TrimSpace(string(out)); err == nil && path != "" {
			return path
		}
	}
	dir := "PowerShell"
	if bin == "powershell" {
		dir = "WindowsPowerShell"
	}
	return filepath.Join(home, "Documents", dir, "Microsoft.PowerShell_profile.ps1")
}

// InstallToProfile writes (or refreshes) the sax-completion block in the given
// shell's profile. It is idempotent: an existing block is replaced in place,
// otherwise the block is appended. Returns the profile path and whether the
// file content changed.
func InstallToProfile(shell string) (path string, changed bool, err error) {
	line, err := initLine(shell)
	if err != nil {
		return "", false, err
	}
	path, err = ProfilePath(shell)
	if err != nil {
		return "", false, err
	}

	block := blockStart + "\n" + line + "\n" + blockEnd

	existing := ""
	if data, err := os.ReadFile(path); err == nil {
		existing = string(data)
	} else if !os.IsNotExist(err) {
		return path, false, fmt.Errorf("read profile: %w", err)
	}

	updated := replaceBlock(existing, block)
	if updated == existing {
		return path, false, nil
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return path, false, fmt.Errorf("create profile dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(updated), 0644); err != nil {
		return path, false, fmt.Errorf("write profile: %w", err)
	}
	return path, true, nil
}

// replaceBlock returns content with the fenced sax-completion block replaced by
// block, or with block appended if no fenced block is present.
func replaceBlock(content, block string) string {
	start := strings.Index(content, blockStart)
	if start >= 0 {
		end := strings.Index(content[start:], blockEnd)
		if end >= 0 {
			end = start + end + len(blockEnd)
			return content[:start] + block + content[end:]
		}
	}
	if content == "" {
		return block + "\n"
	}
	sep := "\n"
	if strings.HasSuffix(content, "\n") {
		sep = ""
	}
	return content + sep + "\n" + block + "\n"
}
