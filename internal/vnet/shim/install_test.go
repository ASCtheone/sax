package shim

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestVersionIsStable(t *testing.T) {
	v := Version()
	if len(v) != versionLength || v != Version() {
		t.Errorf("Version() = %q, want stable %d-char hash", v, versionLength)
	}
}

func TestInstall(t *testing.T) {
	if runtime.GOOS != "darwin" {
		if _, err := Install(t.TempDir()); !errors.Is(err, ErrUnsupported) {
			t.Errorf("err = %v, want ErrUnsupported", err)
		}
		return
	}
	if _, err := exec.LookPath("cc"); err != nil {
		t.Skip("no C compiler")
	}

	root := t.TempDir()
	p, err := Install(root)
	if err != nil {
		t.Fatal(err)
	}
	if p.Dir != filepath.Join(root, Version()) {
		t.Errorf("Dir = %s", p.Dir)
	}
	for _, f := range []string{p.Lib, p.Reinject, filepath.Join(p.ZDotDir, ".zshenv")} {
		if _, err := os.Stat(f); err != nil {
			t.Errorf("missing %s: %v", f, err)
		}
	}
	zshenv, _ := os.ReadFile(filepath.Join(p.ZDotDir, ".zshenv"))
	if !strings.Contains(string(zshenv), "DYLD_INSERT_LIBRARIES") {
		t.Error(".zshenv does not re-export the shim")
	}

	// Second install reuses the cached build.
	before, _ := os.Stat(p.Lib)
	p2, err := Install(root)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(p2.Lib)
	if p2 != p || !before.ModTime().Equal(after.ModTime()) {
		t.Error("second Install rebuilt the shim")
	}

	// No build leftovers.
	entries, _ := os.ReadDir(root)
	if len(entries) != 1 {
		t.Errorf("root has %d entries, want only the version dir", len(entries))
	}
}

func TestInstall_MissingCompiler(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("darwin only")
	}
	t.Setenv("CC", "definitely-not-a-compiler")
	_, err := Install(t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "xcode-select") {
		t.Errorf("err = %v, want hint about xcode-select", err)
	}
}

func TestInstall_CompileFailure(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("darwin only")
	}
	t.Setenv("CC", "false") // exists, always fails
	root := t.TempDir()
	_, err := Install(root)
	if err == nil || !strings.Contains(err.Error(), "compile shim") {
		t.Fatalf("err = %v, want compile error", err)
	}
	if entries, _ := os.ReadDir(root); len(entries) != 0 {
		t.Errorf("failed build left %d entries behind", len(entries))
	}
}
