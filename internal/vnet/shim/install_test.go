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
	if _, ok := currentPlatform(); !ok {
		t.Skip("no shim backend on " + runtime.GOOS)
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
	if _, err := os.Stat(p.Lib); err != nil {
		t.Errorf("missing %s: %v", p.Lib, err)
	}
	if runtime.GOOS == "darwin" {
		for _, f := range []string{p.Reinject, filepath.Join(p.ZDotDir, ".zshenv")} {
			if _, err := os.Stat(f); err != nil {
				t.Errorf("missing %s: %v", f, err)
			}
		}
		zshenv, _ := os.ReadFile(filepath.Join(p.ZDotDir, ".zshenv"))
		if !strings.Contains(string(zshenv), "DYLD_INSERT_LIBRARIES") {
			t.Error(".zshenv does not re-export the shim")
		}
	} else if p.Reinject != "" || p.ZDotDir != "" {
		t.Error("SIP helpers are macOS only")
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

func TestInstall_Unsupported(t *testing.T) {
	orig := goos
	goos = "plan9"
	defer func() { goos = orig }()
	if _, err := Install(t.TempDir()); !errors.Is(err, ErrUnsupported) {
		t.Errorf("err = %v, want ErrUnsupported", err)
	}
}

func TestInstall_MissingCompiler(t *testing.T) {
	if _, ok := currentPlatform(); !ok {
		t.Skip("no shim backend")
	}
	t.Setenv("CC", "definitely-not-a-compiler")
	_, err := Install(t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "C compiler") {
		t.Errorf("err = %v, want a hint about installing a C compiler", err)
	}
}

func TestInstall_CompileFailure(t *testing.T) {
	if _, ok := currentPlatform(); !ok {
		t.Skip("no shim backend")
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
