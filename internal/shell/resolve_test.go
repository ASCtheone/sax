package shell

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func testShell(t *testing.T, dir string) *Shell {
	t.Helper()
	s, err := New(strings.NewReader(""), io.Discard, io.Discard, dir)
	if err != nil {
		t.Fatalf("new shell: %v", err)
	}
	return s
}

// writeExecutable creates a runnable file for the current platform and returns
// the base name a user would type (without extension on Windows).
func writeExecutable(t *testing.T, dir, base string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		name := base + ".cmd"
		if err := os.WriteFile(filepath.Join(dir, name), []byte("@echo off\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return base
	}
	if err := os.WriteFile(filepath.Join(dir, base), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return base
}

func TestResolveBareNameInCwd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Setenv("PATHEXT", ".COM;.EXE;.BAT;.CMD")
	}
	dir := t.TempDir()
	name := writeExecutable(t, dir, "mytool")
	s := testShell(t, dir)

	got, ok := s.resolveCommand(name)
	if !ok {
		t.Fatalf("resolveCommand(%q) not found; expected the cwd executable", name)
	}
	if !strings.HasPrefix(filepath.Base(got), "mytool") {
		t.Errorf("resolved %q, want a path to mytool", got)
	}
}

func TestResolveRelativePath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Setenv("PATHEXT", ".COM;.EXE;.BAT;.CMD")
	}
	dir := t.TempDir()
	writeExecutable(t, dir, "mytool")
	s := testShell(t, dir)

	// "./mytool" should resolve the same as the bare name.
	if _, ok := s.resolveCommand("./mytool"); !ok {
		t.Errorf("resolveCommand(./mytool) not found")
	}
}

func TestResolveNonExecutableNotFound(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Setenv("PATHEXT", ".COM;.EXE;.BAT;.CMD")
	}
	dir := t.TempDir()
	// A .txt file is not runnable.
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := testShell(t, dir)
	if _, ok := s.resolveCommand("notes"); ok {
		t.Errorf("resolveCommand(notes) should not match notes.txt")
	}
	if _, ok := s.resolveCommand("nonexistent"); ok {
		t.Errorf("resolveCommand(nonexistent) should be not found")
	}
}

func TestResolvePathCommand(t *testing.T) {
	// A command that is reliably on PATH for the platform should resolve.
	name := "sh"
	if runtime.GOOS == "windows" {
		name = "cmd"
	}
	s := testShell(t, t.TempDir())
	if _, ok := s.resolveCommand(name); !ok {
		t.Errorf("resolveCommand(%q) should resolve from PATH", name)
	}
}
