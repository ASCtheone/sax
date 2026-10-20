//go:build linux

package shim

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// Wherever supervision is blocked, the supervisor must hand over to the
// LD_PRELOAD shim rather than run the command unshimmed.
func TestSupervisorFallsBackToPreload(t *testing.T) {
	if _, err := exec.LookPath("cc"); err != nil {
		t.Skip("no C compiler")
	}
	p, err := Install(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := exec.Command(p.Supervisor, "--check").Run(); err == nil {
		t.Skip("supervision works here; fallback not exercised")
	}
	cmd := exec.Command(p.Supervisor, "sh", "-c", `echo "$LD_PRELOAD"`)
	cmd.Env = append(os.Environ(), "SAX_VNET_SHIM="+p.Lib, "SAX_VNET_DIR="+t.TempDir(),
		"SAX_VNET_IP=10.12.12.4", "SAX_VNET_SUBNET=10.12.12.0/24")
	out, err := cmd.Output()
	if err != nil || strings.TrimSpace(string(out)) != p.Lib {
		t.Errorf("fallback LD_PRELOAD = %q, %v; want %s", out, err, p.Lib)
	}
}
