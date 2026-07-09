package history

import (
	"path/filepath"
	"testing"
)

func TestAddSkipsBlanksAndConsecutiveDups(t *testing.T) {
	h := New("", 0)
	h.Add("ls")
	h.Add("ls")  // consecutive dup -> skipped
	h.Add("")    // blank -> skipped
	h.Add("  ")  // whitespace -> skipped
	h.Add("pwd")
	h.Add("ls") // non-consecutive dup -> kept
	if h.Len() != 3 {
		t.Fatalf("Len = %d, want 3", h.Len())
	}
	want := []string{"ls", "pwd", "ls"}
	for i, w := range want {
		if h.At(i) != w {
			t.Errorf("At(%d) = %q, want %q", i, h.At(i), w)
		}
	}
}

func TestPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".ss_history")
	h := New(path, 0)
	h.Add("first")
	h.Add("second")

	reloaded := New(path, 0)
	if err := reloaded.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if reloaded.Len() != 2 || reloaded.At(0) != "first" || reloaded.At(1) != "second" {
		t.Fatalf("reloaded = %d entries [%q, %q]", reloaded.Len(), reloaded.At(0), reloaded.At(1))
	}
}

func TestSearchBackward(t *testing.T) {
	h := New("", 0)
	h.Add("git status")
	h.Add("ls -la")
	h.Add("git commit")

	// Most recent match for "git" is "git commit" (index 2).
	if got := h.SearchBackward("git", h.Len()-1); got != 2 {
		t.Errorf("SearchBackward(git) = %d, want 2", got)
	}
	// Searching before index 2 finds "git status" (index 0).
	if got := h.SearchBackward("git", 1); got != 0 {
		t.Errorf("SearchBackward(git, 1) = %d, want 0", got)
	}
	// Case-insensitive.
	if got := h.SearchBackward("STATUS", h.Len()-1); got != 0 {
		t.Errorf("SearchBackward(STATUS) = %d, want 0", got)
	}
	// No match.
	if got := h.SearchBackward("zzz", h.Len()-1); got != -1 {
		t.Errorf("SearchBackward(zzz) = %d, want -1", got)
	}
}

func TestMaxTrim(t *testing.T) {
	h := New("", 3)
	for _, c := range []string{"a", "b", "c", "d", "e"} {
		h.Add(c)
	}
	if h.Len() != 3 {
		t.Fatalf("Len = %d, want 3 (trimmed)", h.Len())
	}
	if h.At(0) != "c" || h.At(2) != "e" {
		t.Errorf("trimmed window = [%q..%q], want [c..e]", h.At(0), h.At(2))
	}
}
