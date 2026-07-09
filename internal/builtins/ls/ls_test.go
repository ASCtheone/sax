package ls_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asc/sax/internal/builtins"
	"github.com/asc/sax/internal/builtins/ls"
)

// makeTree builds a temp directory with a known set of entries and returns it.
func makeTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"b.txt":   "hello",
		"a.txt":   "hi",
		".hidden": "secret",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	return dir
}

// runLS invokes ls with deterministic presentation (no color, single column).
func runLS(dir string, args ...string) (stdout, stderr string, code int) {
	var out, errb bytes.Buffer
	code = ls.New().Run(&builtins.Env{
		Cwd:    dir,
		Args:   args,
		Stdout: &out,
		Stderr: &errb,
		Width:  0, // single column, deterministic
		Color:  false,
	})
	return out.String(), errb.String(), code
}

func lines(s string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func TestDefaultListing(t *testing.T) {
	dir := makeTree(t)
	out, _, code := runLS(dir)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	got := lines(out)
	want := []string{"sub", "a.txt", "b.txt"} // dirs first, then sorted; hidden excluded
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("default listing = %v, want %v", got, want)
	}
}

func TestAllIncludesDotEntries(t *testing.T) {
	dir := makeTree(t)
	out, _, _ := runLS(dir, "-a")
	got := lines(out)
	// Directories (., .., sub) first, then files.
	want := []string{".", "..", "sub", ".hidden", "a.txt", "b.txt"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("ls -a = %v, want %v", got, want)
	}
}

func TestAlmostAllExcludesDotAndDotDot(t *testing.T) {
	dir := makeTree(t)
	out, _, _ := runLS(dir, "-A")
	got := lines(out)
	want := []string{"sub", ".hidden", "a.txt", "b.txt"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("ls -A = %v, want %v", got, want)
	}
}

func TestNaturalSort(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"file10", "file2", "file1", "file20"} {
		if err := os.WriteFile(filepath.Join(dir, n), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out, _, _ := runLS(dir)
	got := lines(out)
	want := []string{"file1", "file2", "file10", "file20"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("natural sort = %v, want %v", got, want)
	}
}

func TestDirsFirstCanBeDisabled(t *testing.T) {
	dir := makeTree(t)
	out, _, _ := runLS(dir, "--no-group-directories-first")
	got := lines(out)
	want := []string{"a.txt", "b.txt", "sub"} // pure name order
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("ls --no-group-directories-first = %v, want %v", got, want)
	}
}

func TestReverseSort(t *testing.T) {
	dir := makeTree(t)
	out, _, _ := runLS(dir, "-r")
	got := lines(out)
	want := []string{"sub", "b.txt", "a.txt"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("ls -r = %v, want %v", got, want)
	}
}

func TestLongFormat(t *testing.T) {
	dir := makeTree(t)
	out, _, code := runLS(dir, "-l")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	got := lines(out)
	if len(got) != 3 {
		t.Fatalf("ls -l line count = %d, want 3:\n%s", len(got), out)
	}
	// Find the b.txt line (order is dirs-first: sub, a.txt, b.txt) and check it
	// carries the right size and a mode string.
	var bLine string
	for _, l := range got {
		if strings.HasSuffix(l, "b.txt") {
			bLine = l
		}
	}
	if bLine == "" {
		t.Fatalf("no b.txt line in:\n%s", out)
	}
	if !strings.Contains(bLine, "5") { // b.txt is "hello" = 5 bytes
		t.Errorf("b.txt long line missing size: %q", bLine)
	}
	if !strings.HasPrefix(bLine, "-") { // regular-file mode string
		t.Errorf("b.txt long line missing mode: %q", bLine)
	}
}

func TestClassifyAppendsSlashForDir(t *testing.T) {
	dir := makeTree(t)
	out, _, _ := runLS(dir, "-F")
	if !strings.Contains(out, "sub/") {
		t.Errorf("ls -F should append / to directories; got:\n%s", out)
	}
}

func TestNonexistentPath(t *testing.T) {
	dir := makeTree(t)
	_, errOut, code := runLS(dir, "nope-does-not-exist")
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(errOut, "cannot access") {
		t.Errorf("stderr = %q, want it to mention 'cannot access'", errOut)
	}
}

func TestInvalidOption(t *testing.T) {
	dir := makeTree(t)
	_, errOut, code := runLS(dir, "-Z")
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(errOut, "invalid option") {
		t.Errorf("stderr = %q, want it to mention 'invalid option'", errOut)
	}
}

func TestColorByType(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "adir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "code.go"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	ls.New().Run(&builtins.Env{Cwd: dir, Stdout: &out, Stderr: &out, Width: 0, Color: true})
	s := out.String()
	if !strings.Contains(s, "\x1b[1;34madir\x1b[0m") {
		t.Errorf("directory not colored blue; got:\n%q", s)
	}
	if !strings.Contains(s, "\x1b[33mcode.go\x1b[0m") {
		t.Errorf(".go file not colored yellow; got:\n%q", s)
	}
}

func TestGitStatusColumn(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-m", "init")
	// Modify the tracked file and add an untracked one.
	if err := os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, _, code := runLS(dir, "--git")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	for _, l := range lines(out) {
		fields := strings.Fields(l)
		if len(fields) < 2 {
			continue
		}
		status, name := fields[0], fields[len(fields)-1]
		switch name {
		case "tracked.txt":
			if !strings.Contains(status, "M") {
				t.Errorf("tracked.txt should show modified (M): %q", l)
			}
		case "untracked.txt":
			if !strings.Contains(status, "N") {
				t.Errorf("untracked.txt should show new (N): %q", l)
			}
		}
	}
}

func TestGitColumnSuppressedOutsideRepo(t *testing.T) {
	dir := makeTree(t)
	out, _, _ := runLS(dir, "--git")
	// Not a repo: no "--" git cells should appear before names.
	if strings.Contains(out, "-- ") {
		t.Errorf("git column should be suppressed outside a repo; got:\n%s", out)
	}
}

func TestColumnsFitWidth(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"aa", "bb", "cc", "dd"} {
		if err := os.WriteFile(filepath.Join(dir, n), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	ls.New().Run(&builtins.Env{Cwd: dir, Stdout: &out, Stderr: &out, Width: 80})
	// All four 2-char names fit on one line at width 80.
	if got := lines(out.String()); len(got) != 1 {
		t.Errorf("expected 1 row at width 80, got %d:\n%s", len(got), out.String())
	}
}
