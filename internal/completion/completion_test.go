package completion

import (
	"strings"
	"testing"
)

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func TestFilterPrefix(t *testing.T) {
	in := []string{"build", "test", "bundle", "build", ""}
	got := filterPrefix(in, "bu")
	want := []string{"build", "bundle"} // sorted + deduped, prefix-filtered
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("filterPrefix = %v, want %v", got, want)
	}
}

func TestSplitPartial(t *testing.T) {
	partial, ctx := splitPartial([]string{"nx", "build", "my"})
	if partial != "my" || strings.Join(ctx, ",") != "nx,build" {
		t.Errorf("splitPartial = (%q, %v)", partial, ctx)
	}
	if p, c := splitPartial(nil); p != "" || c != nil {
		t.Errorf("splitPartial(nil) = (%q, %v)", p, c)
	}
}

func TestSaxCandidatesTopLevel(t *testing.T) {
	// Empty partial → all top-level commands.
	got := SaxCandidates([]string{""})
	for _, want := range []string{"nx", "setup", "completion", "themes", "--kill"} {
		if !contains(got, want) {
			t.Errorf("top-level candidates missing %q: %v", want, got)
		}
	}

	// Prefix narrows it.
	got = SaxCandidates([]string{"co"})
	if !contains(got, "completion") {
		t.Errorf("expected 'completion' for prefix 'co', got %v", got)
	}
	if contains(got, "nx") {
		t.Errorf("prefix 'co' should not include 'nx': %v", got)
	}
}

func TestSaxCandidatesSubcontexts(t *testing.T) {
	if got := SaxCandidates([]string{"completion", ""}); !contains(got, "pwsh") || !contains(got, "zsh") {
		t.Errorf("completion shells = %v", got)
	}
	if got := SaxCandidates([]string{"setup", ""}); !contains(got, "completion") {
		t.Errorf("setup candidates = %v", got)
	}
	if got := SaxCandidates([]string{"themes", ""}); !contains(got, "set") {
		t.Errorf("themes candidates = %v", got)
	}
	if got := SaxCandidates([]string{"themes", "set", ""}); len(got) == 0 {
		t.Error("themes set should list theme presets")
	}
}

func TestSaxCandidatesDelegatesToNx(t *testing.T) {
	// `sax nx <Tab>` should offer nx subcommands.
	got := SaxCandidates([]string{"nx", ""})
	if !contains(got, "build") || !contains(got, "generate") {
		t.Errorf("sax nx delegation missing nx subcommands: %v", got)
	}
}

func TestNxCandidatesSubcommands(t *testing.T) {
	got := NxCandidates([]string{""})
	for _, want := range []string{"build", "test", "serve", "run", "generate", "graph"} {
		if !contains(got, want) {
			t.Errorf("nx subcommands missing %q: %v", want, got)
		}
	}
	got = NxCandidates([]string{"gen"})
	if !contains(got, "generate") {
		t.Errorf("prefix 'gen' should include 'generate': %v", got)
	}
}

func TestNxCandidatesShow(t *testing.T) {
	got := NxCandidates([]string{"show", ""})
	if !contains(got, "projects") || !contains(got, "project") {
		t.Errorf("nx show candidates = %v", got)
	}
}

func TestScriptShells(t *testing.T) {
	for _, sh := range []string{"zsh", "bash", "pwsh", "powershell"} {
		s, err := Script(sh)
		if err != nil {
			t.Errorf("Script(%q) error: %v", sh, err)
		}
		if !strings.Contains(s, "__complete") {
			t.Errorf("Script(%q) missing __complete wiring", sh)
		}
	}
	if _, err := Script("fish"); err == nil {
		t.Error("Script(fish) should error (unsupported)")
	}
}

func TestReplaceBlockInsertAndIdempotent(t *testing.T) {
	block := blockStart + "\nINIT\n" + blockEnd

	// Insert into empty.
	got := replaceBlock("", block)
	if !strings.Contains(got, "INIT") {
		t.Fatalf("insert into empty failed: %q", got)
	}

	// Append to existing content.
	got = replaceBlock("export FOO=bar\n", block)
	if !strings.Contains(got, "export FOO=bar") || !strings.Contains(got, "INIT") {
		t.Fatalf("append failed: %q", got)
	}

	// Replacing an existing block in place must not duplicate it.
	newBlock := blockStart + "\nINIT2\n" + blockEnd
	got2 := replaceBlock(got, newBlock)
	if strings.Count(got2, blockStart) != 1 {
		t.Errorf("expected exactly one block, got %d:\n%s", strings.Count(got2, blockStart), got2)
	}
	if !strings.Contains(got2, "INIT2") || strings.Contains(got2, "INIT\n") {
		t.Errorf("block not replaced cleanly: %q", got2)
	}
}

func TestInitLineShells(t *testing.T) {
	for _, sh := range []string{"zsh", "bash", "pwsh"} {
		line, err := initLine(sh)
		if err != nil || !strings.Contains(line, "sax completion") {
			t.Errorf("initLine(%q) = %q, %v", sh, line, err)
		}
	}
	if _, err := initLine("fish"); err == nil {
		t.Error("initLine(fish) should error")
	}
}
