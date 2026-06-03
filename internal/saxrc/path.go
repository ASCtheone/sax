package saxrc

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// DefaultPath returns the path sax will load the rc file from:
//
//  1. $SAXRC if set (used verbatim, even if missing).
//  2. ~/.saxrc if it exists (the zsh-style location).
//  3. <config-dir>/saxrc otherwise (%APPDATA%\sax\saxrc on Windows,
//     ~/.sax/saxrc elsewhere).
func DefaultPath() string {
	if p := os.Getenv("SAXRC"); p != "" {
		return p
	}
	if home, err := os.UserHomeDir(); err == nil {
		p := filepath.Join(home, ".saxrc")
		if fileExists(p) {
			return p
		}
	}
	return filepath.Join(configDir(), "saxrc")
}

// configDir mirrors config.configDir so saxrc has no dependency on the config
// package (which keeps the dependency graph acyclic).
func configDir() string {
	switch runtime.GOOS {
	case "windows":
		appData := os.Getenv("APPDATA")
		if appData == "" {
			appData = filepath.Join(os.Getenv("USERPROFILE"), "AppData", "Roaming")
		}
		return filepath.Join(appData, "sax")
	default:
		home, _ := os.UserHomeDir()
		return filepath.Join(home, ".sax")
	}
}

// ExpandHome expands a leading "~" or "~/" to the user's home directory.
func ExpandHome(p string) string {
	if p == "" {
		return ""
	}
	if p == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
		return p
	}
	if strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

func fileExists(p string) bool {
	info, err := os.Stat(p)
	return err == nil && !info.IsDir()
}
