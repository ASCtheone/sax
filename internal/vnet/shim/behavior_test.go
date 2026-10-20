package shim

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestShimBehavior runs a real libc program under the installed shim: a
// server binds 0.0.0.0:3200 as 10.12.12.4 and a client in the same router
// reaches it by name. This is what catches hook bugs (e.g. a hook calling
// itself on Linux) that the Go unit tests cannot see.
func TestShimBehavior(t *testing.T) {
	plat, ok := currentPlatform()
	if !ok {
		t.Skip("no shim backend on " + runtime.GOOS)
	}
	if _, err := exec.LookPath("cc"); err != nil {
		t.Skip("no C compiler")
	}
	p, err := Install(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	bin := filepath.Join(t.TempDir(), "echo")
	if out, err := exec.Command("cc", "-O0", "-o", bin, "testdata/echo.c").CombinedOutput(); err != nil {
		t.Fatalf("build echo helper: %v\n%s", err, out)
	}

	state := t.TempDir()
	if err := os.MkdirAll(filepath.Join(state, "ports"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(state, "names"), []byte("api 10.12.12.4\n"), 0o644)
	_ = os.WriteFile(filepath.Join(state, "svc"), nil, 0o644)

	preload := "LD_PRELOAD"
	if plat.sipHelpers {
		preload = "DYLD_INSERT_LIBRARIES"
	}
	env := func(ip string) []string {
		return append(os.Environ(), preload+"="+p.Lib, "SAX_VNET_SHIM="+p.Lib,
			"SAX_VNET_DIR="+state, "SAX_VNET_IP="+ip, "SAX_VNET_SUBNET=10.12.12.0/24")
	}

	server := exec.Command(bin, "serve", "3200")
	server.Env = env("10.12.12.4")
	stdout, _ := server.StdoutPipe()
	server.Stderr = os.Stderr
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Process.Kill(); _ = server.Wait() })

	lines := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(stdout).ReadString('\n')
		lines <- strings.TrimSpace(line)
	}()
	select {
	case line := <-lines:
		if line != "listening 3200" {
			t.Fatalf("server reported %q, want the virtual port 3200", line)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not start (crashed under the shim?)")
	}
	if _, err := os.Stat(filepath.Join(state, "ports", "10.12.12.4_3200")); err != nil {
		t.Errorf("listener not registered: %v", err)
	}

	client := exec.Command(bin, "call", "api", "3200", "ping-through-vnet")
	client.Env = env("10.12.12.9")
	out, err := client.CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "ping-through-vnet" {
		t.Errorf("client = %q, %v", out, err)
	}
}

// TestSupervisorGoBinaries is the reason the supervisor exists: Go programs
// make raw syscalls, so LD_PRELOAD never sees them. Under the supervisor a
// Go server binds :3200 as 10.12.12.4 (reporting the virtual address) and a
// Go client in the same router reaches it.
func TestSupervisorGoBinaries(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the seccomp supervisor is Linux only")
	}
	if _, err := exec.LookPath("cc"); err != nil {
		t.Skip("no C compiler")
	}
	p, err := Install(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if p.Supervisor == "" {
		t.Fatal("Install did not build the supervisor")
	}
	if err := exec.Command(p.Supervisor, "--check").Run(); err != nil {
		t.Skip("seccomp supervision is not permitted here (Yama ptrace_scope >= 2 or a " +
			"container seccomp profile blocking pidfd_getfd); sessions fall back to LD_PRELOAD")
	}

	bin := filepath.Join(t.TempDir(), "goecho")
	build := exec.Command("go", "build", "-o", bin, "./testdata/goecho")
	build.Env = append(os.Environ(), "CGO_ENABLED=0") // fully static: no libc at all
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build goecho: %v\n%s", err, out)
	}

	state := t.TempDir()
	_ = os.MkdirAll(filepath.Join(state, "ports"), 0o755)
	_ = os.WriteFile(filepath.Join(state, "names"), nil, 0o644)
	_ = os.WriteFile(filepath.Join(state, "svc"), nil, 0o644)
	env := func(ip string) []string {
		return append(os.Environ(), "SAX_VNET_SHIM="+p.Lib, "SAX_VNET_DIR="+state,
			"SAX_VNET_IP="+ip, "SAX_VNET_SUBNET=10.12.12.0/24")
	}

	server := exec.Command(p.Supervisor, bin, "serve", "3200")
	server.Env = env("10.12.12.4")
	server.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, _ := server.StdoutPipe()
	server.Stderr = os.Stderr
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(-server.Process.Pid, syscall.SIGKILL) // supervisor and its child
		_ = server.Wait()
	})

	lines := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(stdout).ReadString('\n')
		lines <- strings.TrimSpace(line)
	}()
	select {
	case line := <-lines:
		if !strings.Contains(line, "10.12.12.4:3200") {
			t.Fatalf("server reported %q, want the virtual address 10.12.12.4:3200", line)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Go server did not start under the supervisor")
	}
	if _, err := os.Stat(filepath.Join(state, "ports", "10.12.12.4_3200")); err != nil {
		t.Errorf("listener not registered: %v", err)
	}

	client := exec.Command(p.Supervisor, bin, "call", "10.12.12.4:3200", "go-through-vnet")
	client.Env = env("10.12.12.9")
	out, err := client.CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "go-through-vnet" {
		t.Errorf("Go client = %q, %v", out, err)
	}

	// Outside a router the supervisor just runs the command.
	plain := exec.Command(p.Supervisor, "sh", "-c", "exit 7")
	if err := plain.Run(); err == nil || plain.ProcessState.ExitCode() != 7 {
		t.Errorf("exit status not passed through: %v", err)
	}
}
