package echo_test

import (
	"bytes"
	"testing"

	"github.com/asc/sax/internal/builtins"
	"github.com/asc/sax/internal/builtins/echo"
)

func run(args ...string) (string, int) {
	var out bytes.Buffer
	code := echo.New().Run(&builtins.Env{Args: args, Stdout: &out, Stderr: &out})
	return out.String(), code
}

func TestEchoBasic(t *testing.T) {
	got, code := run("hello", "world")
	if code != 0 || got != "hello world\n" {
		t.Errorf("echo hello world = %q (code %d), want %q", got, code, "hello world\n")
	}
}

func TestEchoNoNewline(t *testing.T) {
	got, _ := run("-n", "hi")
	if got != "hi" {
		t.Errorf("echo -n hi = %q, want %q", got, "hi")
	}
}

func TestEchoEscapes(t *testing.T) {
	got, _ := run("-e", `a\tb\nc`)
	if got != "a\tb\nc\n" {
		t.Errorf("echo -e = %q, want %q", got, "a\tb\nc\n")
	}
}

func TestEchoEscapesOffByDefault(t *testing.T) {
	got, _ := run(`a\tb`)
	if got != `a\tb`+"\n" {
		t.Errorf("echo without -e = %q, want literal backslash-t", got)
	}
}

func TestEchoCombinedFlags(t *testing.T) {
	got, _ := run("-ne", `x\ty`)
	if got != "x\ty" {
		t.Errorf("echo -ne = %q, want %q", got, "x\ty")
	}
}

func TestEchoEmpty(t *testing.T) {
	got, _ := run()
	if got != "\n" {
		t.Errorf("echo = %q, want newline", got)
	}
}
