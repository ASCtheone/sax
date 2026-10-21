package shim

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// macOS ships /usr/bin/python3 (and friends) as SIP-protected stubs that
// exec the real tool from the active developer directory. dyld drops
// DYLD_INSERT_LIBRARIES when it starts a protected binary, so the real
// Python would run without the shim. A "devbin" folder of links to the
// real tools, put first on the PATH of networked panes (and used by the
// exec hook for absolute /usr/bin paths), skips the stub.
//
// Only interpreters are linked: compilers rely on xcrun setting SDKROOT and
// are not dev servers anyway.
var devTools = []string{"python3", "pip3", "pydoc3", "python", "pip"}

// stubDir is where the protected stubs live; a variable for tests.
var stubDir = "/usr/bin"

// developerDir is the active developer directory: $DEVELOPER_DIR, else what
// xcode-select points at.
var developerDir = func() string {
	if d := os.Getenv("DEVELOPER_DIR"); d != "" {
		return d
	}
	if d, err := os.Readlink("/var/db/xcode_select_link"); err == nil {
		return d
	}
	out, err := exec.Command("xcode-select", "-p").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// devToolLinks maps each stubbed tool to the real binary in dev.
func devToolLinks(dev string) map[string]string {
	links := map[string]string{}
	if dev == "" {
		return links
	}
	dirs := []string{
		filepath.Join(dev, "usr", "bin"),
		filepath.Join(dev, "Toolchains", "XcodeDefault.xctoolchain", "usr", "bin"),
	}
	for _, name := range devTools {
		if _, err := os.Stat(filepath.Join(stubDir, name)); err != nil {
			continue // not a stub here
		}
		for _, dir := range dirs {
			target := filepath.Join(dir, name)
			if st, err := os.Stat(target); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
				links[name] = target
				break
			}
		}
	}
	return links
}

// installDevBin makes the devbin folder for the current developer directory
// under root and returns it ("" when there is nothing to link). Folders are
// named by their content, so concurrent installs and xcode-select switches
// never see a half-built or stale one.
func installDevBin(root string) (string, error) {
	links := devToolLinks(developerDir())
	if len(links) == 0 {
		return "", nil
	}
	names := make([]string, 0, len(links))
	for n := range links {
		names = append(names, n)
	}
	sort.Strings(names)
	h := sha256.New()
	for _, n := range names {
		fmt.Fprintf(h, "%s=%s\x00", n, links[n])
	}
	dir := filepath.Join(root, "devbin-"+hex.EncodeToString(h.Sum(nil))[:versionLength])
	if _, err := os.Stat(dir); err == nil {
		return dir, nil
	}

	if err := os.MkdirAll(root, dirPerm); err != nil {
		return "", fmt.Errorf("create devbin root: %w", err)
	}
	tmp, err := os.MkdirTemp(root, ".devbin-")
	if err != nil {
		return "", fmt.Errorf("create devbin: %w", err)
	}
	defer os.RemoveAll(tmp) // no-op once renamed
	for _, n := range names {
		if err := os.Symlink(links[n], filepath.Join(tmp, n)); err != nil {
			return "", fmt.Errorf("link %s: %w", n, err)
		}
	}
	if err := os.Chmod(tmp, dirPerm); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, dir); err != nil {
		if _, statErr := os.Stat(dir); statErr == nil {
			return dir, nil // another process made the same folder
		}
		return "", fmt.Errorf("install devbin: %w", err)
	}
	return dir, nil
}
