package saxrc

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestNormalizeKey(t *testing.T) {
	tests := []struct {
		spec string
		want string
	}{
		{"C-a", "ctrl+a"},
		{"C-A", "ctrl+a"},
		{"ctrl-a", "ctrl+a"},
		{"M-x", "alt+x"},
		{"A-x", "alt+x"},
		{"C-M-x", "ctrl+alt+x"},
		{"S-tab", "shift+tab"},
		{"X", "X"},
		{"c", "c"},
		{"|", "|"},
		{"-", "-"},
		{"C--", "ctrl+-"},
		{"space", "space"},
		{"enter", "enter"},
		{"esc", "esc"},
		{"", ""},
		{"   ", ""},
	}
	for _, tt := range tests {
		t.Run(tt.spec, func(t *testing.T) {
			if got := NormalizeKey(tt.spec); got != tt.want {
				t.Errorf("NormalizeKey(%q) = %q, want %q", tt.spec, got, tt.want)
			}
		})
	}
}

func TestTokenize(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"set shell zsh", []string{"set", "shell", "zsh"}},
		{`alias work="sax -ca work"`, []string{"alias", "work=sax -ca work"}},
		{`run "echo hi there"`, []string{"run", "echo hi there"}},
		{"set  theme   gruvbox", []string{"set", "theme", "gruvbox"}},
		{`bind '"' window-list`, []string{"bind", `"`, "window-list"}},
		{"", nil},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := tokenize(tt.in); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("tokenize(%q) = %#v, want %#v", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseOptionsAndEnv(t *testing.T) {
	content := `
# a comment
set shell zsh
set prefix C-a
set theme tokyo-night
set theme.accent #ff9e64
set history-limit 5000
set mouse on
setenv EDITOR nvim
export PAGER=less
`
	cfg := Parse(content, "")

	if len(cfg.Warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", cfg.Warnings)
	}
	if cfg.Shell() != "zsh" {
		t.Errorf("Shell() = %q, want zsh", cfg.Shell())
	}
	if cfg.Prefix() != "ctrl+a" {
		t.Errorf("Prefix() = %q, want ctrl+a", cfg.Prefix())
	}
	if cfg.ThemeName() != "tokyo-night" {
		t.Errorf("ThemeName() = %q, want tokyo-night", cfg.ThemeName())
	}
	if ov := cfg.ThemeOverrides(); ov["accent"] != "#ff9e64" {
		t.Errorf("ThemeOverrides()[accent] = %q, want #ff9e64", ov["accent"])
	}
	if n, ok := cfg.HistoryLimit(); !ok || n != 5000 {
		t.Errorf("HistoryLimit() = %d,%v want 5000,true", n, ok)
	}
	if v, ok := cfg.BoolOption("mouse"); !ok || !v {
		t.Errorf("BoolOption(mouse) = %v,%v want true,true", v, ok)
	}

	wantEnv := []EnvVar{{"EDITOR", "nvim"}, {"PAGER", "less"}}
	if !reflect.DeepEqual(cfg.Env, wantEnv) {
		t.Errorf("Env = %#v, want %#v", cfg.Env, wantEnv)
	}
	wantStrings := []string{"EDITOR=nvim", "PAGER=less"}
	if !reflect.DeepEqual(cfg.EnvStrings(), wantStrings) {
		t.Errorf("EnvStrings() = %#v, want %#v", cfg.EnvStrings(), wantStrings)
	}
}

func TestParseAliasesBindsHooksRun(t *testing.T) {
	content := `
alias work="new-session work"
bind r reload
bind | split-v
unbind '"'
hook new-pane "echo welcome"
hook new-pane "ls"
run "split-v"
run "split-h"
`
	cfg := Parse(content, "")
	if len(cfg.Warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", cfg.Warnings)
	}

	if cfg.Aliases["work"] != "new-session work" {
		t.Errorf("alias work = %q", cfg.Aliases["work"])
	}
	wantBinds := []Bind{{"r", "reload"}, {"|", "split-v"}}
	if !reflect.DeepEqual(cfg.Binds, wantBinds) {
		t.Errorf("Binds = %#v, want %#v", cfg.Binds, wantBinds)
	}
	if !reflect.DeepEqual(cfg.Unbinds, []string{`"`}) {
		t.Errorf("Unbinds = %#v", cfg.Unbinds)
	}
	wantHooks := []string{"echo welcome", "ls"}
	if !reflect.DeepEqual(cfg.Hooks["new-pane"], wantHooks) {
		t.Errorf("Hooks[new-pane] = %#v, want %#v", cfg.Hooks["new-pane"], wantHooks)
	}
	if !reflect.DeepEqual(cfg.RunCmds, []string{"split-v", "split-h"}) {
		t.Errorf("RunCmds = %#v", cfg.RunCmds)
	}
}

func TestParseFunction(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []string
	}{
		{"inline", "function dev { split-v; split-h }", []string{"split-v", "split-h"}},
		{"multiline", "function dev {\n  split-v\n  split-h\n}", []string{"split-v", "split-h"}},
		{"brace-next-line", "function dev\n{\nsplit-v\n}", []string{"split-v"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Parse(tt.content, "")
			if !reflect.DeepEqual(cfg.Funcs["dev"], tt.want) {
				t.Errorf("Funcs[dev] = %#v, want %#v (warnings: %v)", cfg.Funcs["dev"], tt.want, cfg.Warnings)
			}
		})
	}
}

func TestLineContinuation(t *testing.T) {
	content := "run \"echo one \\\n two\""
	cfg := Parse(content, "")
	if len(cfg.RunCmds) != 1 || cfg.RunCmds[0] != "echo one  two" {
		t.Errorf("RunCmds = %#v", cfg.RunCmds)
	}
}

func TestWarningsOnMalformed(t *testing.T) {
	content := `
set
bind
unknownthing foo
export NOEQUALS
`
	cfg := Parse(content, "")
	if len(cfg.Warnings) != 4 {
		t.Errorf("got %d warnings, want 4: %v", len(cfg.Warnings), cfg.Warnings)
	}
}

func TestSourceInclude(t *testing.T) {
	dir := t.TempDir()
	extra := filepath.Join(dir, "extra.saxrc")
	if err := os.WriteFile(extra, []byte("set theme gruvbox\n"), 0600); err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(dir, "main.saxrc")
	if err := os.WriteFile(main, []byte("set shell bash\nsource extra.saxrc\n"), 0600); err != nil {
		t.Fatal(err)
	}

	cfg := LoadPath(main)
	if cfg.Shell() != "bash" {
		t.Errorf("Shell() = %q, want bash", cfg.Shell())
	}
	if cfg.ThemeName() != "gruvbox" {
		t.Errorf("ThemeName() = %q, want gruvbox (from source)", cfg.ThemeName())
	}
}

func TestSourceCycle(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.saxrc")
	b := filepath.Join(dir, "b.saxrc")
	os.WriteFile(a, []byte("source b.saxrc\n"), 0600)
	os.WriteFile(b, []byte("source a.saxrc\n"), 0600)

	cfg := LoadPath(a)
	if len(cfg.Warnings) == 0 {
		t.Error("expected a cycle warning, got none")
	}
}

func TestParseStripsBOM(t *testing.T) {
	// A UTF-8 BOM (common on Windows-authored files) must not corrupt the
	// first directive.
	content := "\ufeffset shell zsh\n"
	cfg := Parse(content, "")
	if len(cfg.Warnings) != 0 {
		t.Fatalf("BOM produced warnings: %v", cfg.Warnings)
	}
	if cfg.Shell() != "zsh" {
		t.Errorf("Shell() = %q, want zsh", cfg.Shell())
	}
}

func TestLoadMissingFileIsEmpty(t *testing.T) {
	cfg := LoadPath(filepath.Join(t.TempDir(), "does-not-exist.saxrc"))
	if cfg == nil {
		t.Fatal("LoadPath returned nil")
	}
	if len(cfg.Warnings) != 0 {
		t.Errorf("missing file should not warn, got %v", cfg.Warnings)
	}
	if cfg.Shell() != "" {
		t.Errorf("empty config Shell() = %q", cfg.Shell())
	}
}

func TestNilConfigAccessors(t *testing.T) {
	var c *Config
	if c.Shell() != "" || c.Prefix() != "" {
		t.Error("nil config accessors should return zero values")
	}
	if c.EnvStrings() != nil || c.ThemeOverrides() != nil {
		t.Error("nil config slice/map accessors should return nil")
	}
}
