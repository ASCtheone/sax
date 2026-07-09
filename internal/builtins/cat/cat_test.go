package cat_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/asc/sax/internal/builtins"
	"github.com/asc/sax/internal/builtins/cat"
)

func run(dir string, args ...string) (string, string, int) {
	var out, errb bytes.Buffer
	code := cat.New().Run(&builtins.Env{Cwd: dir, Args: args, Stdout: &out, Stderr: &errb})
	return out.String(), errb.String(), code
}

func TestCatFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\n"), 0o644)
	out, _, code := run(dir, "a.txt")
	if code != 0 || out != "hello\n" {
		t.Errorf("cat a.txt = %q (code %d), want %q", out, code, "hello\n")
	}
}

func TestCatMultiple(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a"), []byte("A"), 0o644)
	os.WriteFile(filepath.Join(dir, "b"), []byte("B"), 0o644)
	out, _, _ := run(dir, "a", "b")
	if out != "AB" {
		t.Errorf("cat a b = %q, want AB", out)
	}
}

func TestCatMissing(t *testing.T) {
	dir := t.TempDir()
	_, errOut, code := run(dir, "nope")
	if code != 1 || errOut == "" {
		t.Errorf("cat missing: code=%d err=%q, want code 1 + message", code, errOut)
	}
}

func TestCatDirectory(t *testing.T) {
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "sub"), 0o755)
	_, errOut, code := run(dir, "sub")
	if code != 1 || errOut == "" {
		t.Errorf("cat dir: code=%d err=%q, want code 1 + 'Is a directory'", code, errOut)
	}
}

func TestCatNoArgs(t *testing.T) {
	_, _, code := run(t.TempDir())
	if code != 1 {
		t.Errorf("cat with no args = %d, want 1", code)
	}
}
