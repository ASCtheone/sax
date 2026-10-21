//go:build darwin

package shim

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Apple's /usr/bin/python3 is a SIP-protected stub: started directly it
// drops DYLD_INSERT_LIBRARIES before exec'ing the real Python. The exec hook
// sends it to the real tool in devbin instead, also as a script interpreter.
func TestExecSIPDevTools(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles the shim")
	}
	if _, err := os.Stat("/usr/bin/python3"); err != nil {
		t.Skip("no /usr/bin/python3")
	}
	if _, err := exec.LookPath("cc"); err != nil {
		t.Skip("no C compiler")
	}
	root := t.TempDir()
	p, err := Install(root)
	if err != nil {
		t.Fatal(err)
	}
	if p.DevBin == "" {
		t.Skip("no developer tools to link (xcode-select)")
	}
	bin := filepath.Join(t.TempDir(), "spawn")
	if out, err := exec.Command("cc", "-O0", "-o", bin, "testdata/spawn.c").CombinedOutput(); err != nil {
		t.Fatalf("build spawn: %v\n%s", err, out)
	}

	probe := "import os; print('DYLD=' + str(os.environ.get('DYLD_INSERT_LIBRARIES')))"
	scripts := t.TempDir()
	direct := filepath.Join(scripts, "direct.py")
	viaEnv := filepath.Join(scripts, "env.py")
	_ = os.WriteFile(direct, []byte("#!/usr/bin/python3\n"+probe+"\n"), 0o755)
	_ = os.WriteFile(viaEnv, []byte("#!/usr/bin/env python3\n"+probe+"\n"), 0o755)

	run := func(devbin string, args ...string) string {
		cmd := exec.Command(bin, args...)
		cmd.Env = append(os.Environ(),
			"DYLD_INSERT_LIBRARIES="+p.Lib, "SAX_VNET_SHIM="+p.Lib, "SAX_VNET_DEVBIN="+devbin,
			"PATH=/usr/bin:/bin") // python3 resolves to the stub
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}

	for name, args := range map[string][]string{
		"absolute /usr/bin/python3": {"/usr/bin/python3", "-c", probe},
		"#!/usr/bin/python3 script": {direct},
		"#!/usr/bin/env python3":    {viaEnv},
	} {
		if got := run(p.DevBin, args...); got != "DYLD="+p.Lib {
			t.Errorf("%s: %q, want the shim to survive", name, got)
		}
	}
	if got := run("", "/usr/bin/python3", "-c", probe); got != "DYLD=None" {
		t.Errorf("control without devbin: %q (the stub should drop the shim)", got)
	}
}
