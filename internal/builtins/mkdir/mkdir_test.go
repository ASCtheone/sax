package mkdir_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/asc/sax/internal/builtins"
	"github.com/asc/sax/internal/builtins/mkdir"
)

func run(dir string, args ...string) (string, int) {
	var errb bytes.Buffer
	code := mkdir.New().Run(&builtins.Env{Cwd: dir, Args: args, Stdout: &errb, Stderr: &errb})
	return errb.String(), code
}

func isDir(t *testing.T, path string) bool {
	t.Helper()
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func TestMkdirSimple(t *testing.T) {
	dir := t.TempDir()
	if _, code := run(dir, "new"); code != 0 {
		t.Fatalf("mkdir new code=%d", code)
	}
	if !isDir(t, filepath.Join(dir, "new")) {
		t.Error("directory not created")
	}
}

func TestMkdirParents(t *testing.T) {
	dir := t.TempDir()
	if _, code := run(dir, "-p", "a/b/c"); code != 0 {
		t.Fatalf("mkdir -p code=%d", code)
	}
	if !isDir(t, filepath.Join(dir, "a", "b", "c")) {
		t.Error("nested directories not created")
	}
}

func TestMkdirNestedWithoutPFails(t *testing.T) {
	dir := t.TempDir()
	if _, code := run(dir, "a/b/c"); code == 0 {
		t.Error("mkdir of nested path without -p should fail")
	}
}

func TestMkdirExistingFails(t *testing.T) {
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "exists"), 0o755)
	if out, code := run(dir, "exists"); code == 0 || out == "" {
		t.Errorf("mkdir existing should fail with message, got code=%d out=%q", code, out)
	}
}

func TestMkdirExistingWithPOk(t *testing.T) {
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "exists"), 0o755)
	if _, code := run(dir, "-p", "exists"); code != 0 {
		t.Error("mkdir -p on existing dir should succeed")
	}
}
