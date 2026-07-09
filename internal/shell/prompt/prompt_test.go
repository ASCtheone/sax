package prompt

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderPlainNoRepo(t *testing.T) {
	dir := t.TempDir() // not a git repo
	got := Render(dir, 0, false)
	if !strings.HasPrefix(got, arrow+"  ") {
		t.Errorf("prompt = %q, want it to start with the arrow", got)
	}
	if !strings.Contains(got, filepath.Base(dir)) {
		t.Errorf("prompt = %q, want it to contain the dir basename %q", got, filepath.Base(dir))
	}
	if strings.Contains(got, "git:(") {
		t.Errorf("prompt = %q, should have no git segment outside a repo", got)
	}
}

func TestRenderArrowColorByExit(t *testing.T) {
	dir := t.TempDir()
	ok := Render(dir, 0, true)
	fail := Render(dir, 1, true)
	if !strings.HasPrefix(ok, green) {
		t.Errorf("success prompt should start green: %q", ok)
	}
	if !strings.HasPrefix(fail, red) {
		t.Errorf("failed prompt should start red: %q", fail)
	}
}

func TestRenderGitSegment(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init")
	git("commit", "--allow-empty", "-m", "init")

	// Clean repo: branch shown, no dirty cross.
	clean := Render(dir, 0, false)
	if !strings.Contains(clean, "git:(") {
		t.Fatalf("expected git segment, got %q", clean)
	}
	if strings.Contains(clean, cross) {
		t.Errorf("clean repo should not show the dirty cross: %q", clean)
	}

	// Dirty repo: cross appears.
	if err := os.WriteFile(filepath.Join(dir, "x.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirty := Render(dir, 0, false)
	if !strings.Contains(dirty, cross) {
		t.Errorf("dirty repo should show the cross: %q", dirty)
	}
}
