package shell_test

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	gopty "github.com/aymanbagabas/go-pty"
)

// TestInteractivePTY drives the ss binary through a real pseudo-terminal: it
// types a builtin (pwd), an external command (cmd /c echo ...), and exit, then
// asserts the output. This exercises the interactive engine end to end —
// terminal detection, the input pump, raw-mode passthrough, and PTY-backed
// external exec (a nested ConPTY) — which piped input cannot reach.
func TestInteractivePTY(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY integration test in -short mode")
	}

	ssBin := buildSS(t)

	p, err := gopty.New()
	if err != nil {
		t.Skipf("no pty available in this environment: %v", err)
	}
	defer p.Close()
	if err := p.Resize(80, 24); err != nil {
		t.Fatalf("resize pty: %v", err)
	}

	cmd := p.Command(ssBin)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start ss in pty: %v", err)
	}

	// Read the master continuously into a buffer. On Windows ConPTY the master
	// does not reliably EOF when the child exits, so we time-box instead of
	// waiting for EOF.
	var (
		mu  sync.Mutex
		buf bytes.Buffer
	)
	go func() {
		chunk := make([]byte, 4096)
		for {
			n, err := p.Read(chunk)
			if n > 0 {
				mu.Lock()
				buf.Write(chunk[:n])
				mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()

	// Type into the child's terminal, pacing so each command is processed.
	// CR (\r) is what a real Enter key sends.
	for _, line := range []string{"pwd\r\n", "cmd /c echo hello-pty\r\n", "exit\r\n"} {
		if _, err := io.WriteString(p, line); err != nil {
			t.Fatalf("write to pty: %v", err)
		}
		time.Sleep(400 * time.Millisecond)
	}
	time.Sleep(500 * time.Millisecond)

	mu.Lock()
	out := buf.String()
	mu.Unlock()

	for _, want := range []string{"sax shell", "hello-pty"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected output to contain %q; got:\n%s", want, out)
		}
	}
}

// TestInteractiveCtrlC verifies that Ctrl+C at the prompt cancels the line and
// does NOT kill ss: after the interrupt, ss must still execute commands. If the
// signal handler were missing, the 0x03 would terminate ss and the command
// after it would never run.
func TestInteractiveCtrlC(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY integration test in -short mode")
	}

	ssBin := buildSS(t)

	p, err := gopty.New()
	if err != nil {
		t.Skipf("no pty available in this environment: %v", err)
	}
	defer p.Close()
	if err := p.Resize(80, 24); err != nil {
		t.Fatalf("resize pty: %v", err)
	}

	cmd := p.Command(ssBin)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start ss in pty: %v", err)
	}

	var (
		mu  sync.Mutex
		buf bytes.Buffer
	)
	go func() {
		chunk := make([]byte, 4096)
		for {
			n, err := p.Read(chunk)
			if n > 0 {
				mu.Lock()
				buf.Write(chunk[:n])
				mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()

	// Type a partial line, send Ctrl+C (should cancel it), then run a command
	// to prove ss is still alive and responsive.
	steps := [][]byte{
		[]byte("half-typed-junk"),
		{0x03}, // Ctrl+C
		[]byte("cmd /c echo survived-ctrlc\r\n"),
		[]byte("exit\r\n"),
	}
	for _, s := range steps {
		if _, err := p.Write(s); err != nil {
			t.Fatalf("write to pty: %v", err)
		}
		time.Sleep(400 * time.Millisecond)
	}
	time.Sleep(500 * time.Millisecond)

	mu.Lock()
	out := buf.String()
	mu.Unlock()

	if !strings.Contains(out, "survived-ctrlc") {
		t.Errorf("ss did not survive Ctrl+C at the prompt; got:\n%s", out)
	}
	if !strings.Contains(out, "^C") {
		t.Errorf("expected ^C echo after interrupt; got:\n%s", out)
	}
}

// TestInteractiveReturnsToPrompt runs two programs in a row and checks that the
// second one executes. If the shell failed to detect the first child's exit
// (e.g. waiting on PTY EOF, which Windows ConPTY may never deliver), it would
// hang after the first command and the second would never run.
func TestInteractiveReturnsToPrompt(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping PTY integration test in -short mode")
	}

	ssBin := buildSS(t)

	p, err := gopty.New()
	if err != nil {
		t.Skipf("no pty available in this environment: %v", err)
	}
	defer p.Close()
	if err := p.Resize(80, 24); err != nil {
		t.Fatalf("resize pty: %v", err)
	}

	cmd := p.Command(ssBin)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start ss in pty: %v", err)
	}

	var (
		mu  sync.Mutex
		buf bytes.Buffer
	)
	go func() {
		chunk := make([]byte, 4096)
		for {
			n, err := p.Read(chunk)
			if n > 0 {
				mu.Lock()
				buf.Write(chunk[:n])
				mu.Unlock()
			}
			if err != nil {
				return
			}
		}
	}()

	for _, line := range []string{"cmd /c echo AAA\r", "cmd /c echo BBB\r", "exit\r"} {
		if _, err := io.WriteString(p, line); err != nil {
			t.Fatalf("write to pty: %v", err)
		}
		time.Sleep(600 * time.Millisecond)
	}
	time.Sleep(500 * time.Millisecond)

	mu.Lock()
	out := buf.String()
	mu.Unlock()

	for _, want := range []string{"AAA", "BBB"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q — shell did not return to the prompt after a command; got:\n%s", want, out)
		}
	}
}

// buildSS compiles the ss binary into a temp dir and returns its path.
func buildSS(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "ss_test.exe")
	// -buildvcs=false avoids a hard failure when git can't stamp VCS info
	// (e.g. the repo lives on a mount git considers "dubious ownership", as on
	// /mnt/c under WSL).
	build := exec.Command("go", "build", "-buildvcs=false", "-o", bin, "github.com/asc/sax/cmd/ss")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		t.Fatalf("build ss: %v", err)
	}
	return bin
}
