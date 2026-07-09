package env_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/asc/sax/internal/builtins"
	"github.com/asc/sax/internal/builtins/env"
)

func TestEnvPrintsSortedVars(t *testing.T) {
	t.Setenv("SS_TEST_ZZZ", "last")
	t.Setenv("SS_TEST_AAA", "first")

	var out bytes.Buffer
	code := env.New().Run(&builtins.Env{Stdout: &out, Stderr: &out})
	if code != 0 {
		t.Fatalf("env code = %d", code)
	}
	s := out.String()
	if !strings.Contains(s, "SS_TEST_AAA=first") || !strings.Contains(s, "SS_TEST_ZZZ=last") {
		t.Errorf("env output missing test vars:\n%s", s)
	}

	// Output must be sorted.
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i := 1; i < len(lines); i++ {
		if lines[i-1] > lines[i] {
			t.Errorf("env output not sorted at line %d: %q > %q", i, lines[i-1], lines[i])
		}
	}
}

func TestEnvWithArgsUnsupported(t *testing.T) {
	var out bytes.Buffer
	code := env.New().Run(&builtins.Env{Args: []string{"FOO=bar", "cmd"}, Stdout: &out, Stderr: &out})
	if code != 2 {
		t.Errorf("env with args should return 2, got %d", code)
	}
}
