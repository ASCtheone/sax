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

	// No build leftovers: the version dir, plus the devbin dir on macOS.
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if e.Name() != Version() && filepath.Join(root, e.Name()) != p.DevBin {
			t.Errorf("unexpected %s in the install root", e.Name())
		}
	}
}

// devbin links the real tools behind the SIP stubs of the active developer
// directory, in a folder named by its content.
func TestInstallDevBin(t *testing.T) {
	dev, stubs := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(dev, "usr", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"python3", "pip3", "clang"} {
		_ = os.WriteFile(filepath.Join(dev, "usr", "bin", name), []byte("#!/bin/sh\n"), 0o755)
		_ = os.WriteFile(filepath.Join(stubs, name), []byte("stub"), 0o755)
	}
	origDev, origStub := developerDir, stubDir
	developerDir, stubDir = func() string { return dev }, stubs
	t.Cleanup(func() { developerDir, stubDir = origDev, origStub })

	root := t.TempDir()
	dir, err := installDevBin(root)
	if err != nil || dir == "" {
		t.Fatalf("installDevBin = %q, %v", dir, err)
	}
	target, err := os.Readlink(filepath.Join(dir, "python3"))
	if err != nil || target != filepath.Join(dev, "usr", "bin", "python3") {
		t.Errorf("python3 -> %q, %v", target, err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "pip3")); err != nil {
		t.Error("pip3 not linked")
	}
	if _, err := os.Lstat(filepath.Join(dir, "clang")); err == nil {
		t.Error("compilers must not be linked (they need xcrun's SDKROOT)")
	}
	if again, _ := installDevBin(root); again != dir {
		t.Errorf("second install = %q, want the same %q", again, dir)
	}

	// Another developer dir (xcode-select -s) gets its own folder.
	_ = os.Remove(filepath.Join(dev, "usr", "bin", "pip3"))
	if other, _ := installDevBin(root); other == dir || other == "" {
		t.Errorf("changed tools should make a new folder, got %q", other)
	}
	developerDir = func() string { return "" }
	if none, err := installDevBin(root); none != "" || err != nil {
		t.Errorf("no developer dir: %q, %v", none, err)
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
