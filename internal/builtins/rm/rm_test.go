package rm_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/asc/sax/internal/builtins"
	"github.com/asc/sax/internal/builtins/rm"
)

func run(dir string, args ...string) (string, int) {
	var errb bytes.Buffer
	code := rm.New().Run(&builtins.Env{Cwd: dir, Args: args, Stdout: &errb, Stderr: &errb})
	return errb.String(), code
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestRmFile(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "x")
	os.WriteFile(f, nil, 0o644)
	if _, code := run(dir, "x"); code != 0 || exists(f) {
		t.Errorf("rm x failed: code=%d exists=%v", code, exists(f))
	}
}

func TestRmMissingFails(t *testing.T) {
	if out, code := run(t.TempDir(), "nope"); code == 0 || out == "" {
		t.Errorf("rm missing should fail, got code=%d out=%q", code, out)
	}
}

func TestRmMissingWithForceOk(t *testing.T) {
	if out, code := run(t.TempDir(), "-f", "nope"); code != 0 || out != "" {
		t.Errorf("rm -f missing should succeed silently, got code=%d out=%q", code, out)
	}
}

func TestRmDirWithoutRecursiveFails(t *testing.T) {
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "d"), 0o755)
	if out, code := run(dir, "d"); code == 0 || out == "" {
		t.Errorf("rm dir without -r should fail, got code=%d out=%q", code, out)
	}
}

func TestRmDirRecursive(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "d")
	os.MkdirAll(filepath.Join(sub, "nested"), 0o755)
	os.WriteFile(filepath.Join(sub, "nested", "f"), nil, 0o644)
	if _, code := run(dir, "-rf", "d"); code != 0 || exists(sub) {
		t.Errorf("rm -rf d failed: code=%d exists=%v", code, exists(sub))
	}
}
