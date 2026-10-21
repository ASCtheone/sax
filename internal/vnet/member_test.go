package vnet

import (
	"strings"
	"testing"

	"github.com/asc/sax/internal/vnet/shim"
)

var testShim = shim.Paths{
	Dir:      "/s",
	Lib:      "/s/libsaxvnet.dylib",
	Reinject: "/s/reinject.cjs",
	ZDotDir:  "/s/zdotdir",
}

func envMap(kvs []string) map[string]string {
	m := map[string]string{}
	for _, kv := range kvs {
		k, v, _ := strings.Cut(kv, "=")
		m[k] = v
	}
	return m
}

func TestHostFromSessionName(t *testing.T) {
	cases := map[string]string{
		"api":       "api",
		"v1.api":    "api",
		"V1.Web-2":  "web-2",
		"proj.v1.x": "x",
		"under_bar": "",
		"v1.":       "",
	}
	for in, want := range cases {
		got, err := HostFromSessionName(in)
		if want == "" {
			if err == nil {
				t.Errorf("HostFromSessionName(%q) = %q, want error", in, got)
			}
			continue
		}
		if err != nil || got != want {
			t.Errorf("HostFromSessionName(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestParseNetFlag(t *testing.T) {
	r, h, err := ParseNetFlag("v1:api")
	if err != nil || r != "v1" || h != "api" {
		t.Errorf("v1:api -> %q %q %v", r, h, err)
	}
	r, h, err = ParseNetFlag("v1")
	if err != nil || r != "v1" || h != "" {
		t.Errorf("v1 -> %q %q %v", r, h, err)
	}
	for _, bad := range []string{"", ":api", "v1:", "V 1", "v1:a b"} {
		if _, _, err := ParseNetFlag(bad); err == nil {
			t.Errorf("ParseNetFlag(%q) expected error", bad)
		}
	}
}

func TestDaemonEnv(t *testing.T) {
	m := Member{Router: "v1", Host: "api", IP: "10.12.12.4", Subnet: "10.12.12.0/24", Dir: "/state/v1"}
	env := envMap(DaemonEnv(m, testShim))
	want := map[string]string{
		EnvDir:      "/state/v1",
		EnvIP:       "10.12.12.4",
		EnvSubnet:   "10.12.12.0/24",
		EnvRouter:   "v1",
		EnvHost:     "api",
		EnvShim:     testShim.Lib,
		EnvReinject: testShim.Reinject,
		EnvZWrapper: testShim.ZDotDir,
	}
	for k, v := range want {
		if env[k] != v {
			t.Errorf("%s = %q, want %q", k, env[k], v)
		}
	}
	if _, ok := env["DYLD_INSERT_LIBRARIES"]; ok {
		t.Error("daemon itself must not load the shim")
	}
}

func TestPaneEnv(t *testing.T) {
	withGOOS(t, "darwin")
	base := map[string]string{
		EnvShim:        testShim.Lib,
		EnvReinject:    testShim.Reinject,
		EnvZWrapper:    testShim.ZDotDir,
		"NODE_OPTIONS": "--max-old-space-size=4096",
		"ZDOTDIR":      "/home/me/.config/zsh",
	}
	env := envMap(PaneEnv(func(k string) string { return base[k] }))

	if env["DYLD_INSERT_LIBRARIES"] != testShim.Lib {
		t.Errorf("DYLD = %q", env["DYLD_INSERT_LIBRARIES"])
	}
	if env["NODE_OPTIONS"] != `--max-old-space-size=4096 --require "/s/reinject.cjs"` {
		t.Errorf("NODE_OPTIONS = %q (must keep the user's options)", env["NODE_OPTIONS"])
	}
	if env["ZDOTDIR"] != testShim.ZDotDir || env[EnvOrigZDotDir] != "/home/me/.config/zsh" {
		t.Errorf("zsh wrapper env = %q / %q", env["ZDOTDIR"], env[EnvOrigZDotDir])
	}
	if !strings.Contains(env["PROMPT_COMMAND"], "DYLD_INSERT_LIBRARIES") {
		t.Errorf("PROMPT_COMMAND = %q", env["PROMPT_COMMAND"])
	}
}

func TestPaneEnv_Idempotent(t *testing.T) {
	withGOOS(t, "darwin")
	base := map[string]string{
		EnvShim:          testShim.Lib,
		EnvReinject:      testShim.Reinject,
		EnvZWrapper:      testShim.ZDotDir,
		"NODE_OPTIONS":   `--require "/s/reinject.cjs"`,
		"PROMPT_COMMAND": "my_prompt",
	}
	env := envMap(PaneEnv(func(k string) string { return base[k] }))
	if env["NODE_OPTIONS"] != `--require "/s/reinject.cjs"` {
		t.Errorf("reinject added twice: %q", env["NODE_OPTIONS"])
	}
	if _, ok := env["PROMPT_COMMAND"]; ok {
		t.Error("existing PROMPT_COMMAND must be left alone")
	}
	if _, ok := env[EnvOrigZDotDir]; ok {
		t.Error("no original ZDOTDIR to remember")
	}
}

func TestPaneEnv_NotNetworked(t *testing.T) {
	if env := PaneEnv(func(string) string { return "" }); env != nil {
		t.Errorf("PaneEnv outside a router = %v, want nil", env)
	}
}

func TestOwners(t *testing.T) {
	r, _, _ := mustRouter(t).WithSession("api", "")
	r2 := r.WithOwner("api", "v1.api")
	if r2.Owners["api"] != "v1.api" || len(r.Owners) != 0 {
		t.Errorf("WithOwner: %v / original %v", r2.Owners, r.Owners)
	}
	r3 := r2.WithoutSession("api")
	if _, ok := r3.Owners["api"]; ok {
		t.Error("WithoutSession should drop the owner too")
	}
}

func TestStripEnv(t *testing.T) {
	withGOOS(t, "darwin")
	daemon := map[string]string{
		EnvShim:        testShim.Lib,
		EnvReinject:    testShim.Reinject,
		EnvZWrapper:    testShim.ZDotDir,
		"NODE_OPTIONS": "--max-old-space-size=4096",
		"ZDOTDIR":      "/home/me/.config/zsh",
	}
	pane := envMap(PaneEnv(func(k string) string { return daemon[k] }))
	for k, v := range daemon {
		if _, set := pane[k]; !set {
			pane[k] = v
		}
	}
	pane[EnvIP] = "10.12.12.4"
	pane["HOME"] = "/home/me"

	var environ []string
	for k, v := range pane {
		environ = append(environ, k+"="+v)
	}
	got := envMap(StripEnv(environ))

	for k := range got {
		if strings.HasPrefix(k, "SAX_VNET_") {
			t.Errorf("%s not stripped", k)
		}
	}
	if _, ok := got["DYLD_INSERT_LIBRARIES"]; ok {
		t.Error("DYLD_INSERT_LIBRARIES not stripped")
	}
	if _, ok := got["PROMPT_COMMAND"]; ok {
		t.Error("our PROMPT_COMMAND not stripped")
	}
	if got["NODE_OPTIONS"] != "--max-old-space-size=4096" {
		t.Errorf("NODE_OPTIONS = %q, want the user's part only", got["NODE_OPTIONS"])
	}
	if got["ZDOTDIR"] != "/home/me/.config/zsh" {
		t.Errorf("ZDOTDIR = %q, want the user's original", got["ZDOTDIR"])
	}
	if got["HOME"] != "/home/me" {
		t.Error("unrelated variables must survive")
	}
}

func TestStripEnv_LeavesForeignValuesAlone(t *testing.T) {
	in := []string{"PROMPT_COMMAND=history -a", "ZDOTDIR=/mine", "NODE_OPTIONS=--inspect",
		"DYLD_INSERT_LIBRARIES=/my/own.dylib"}
	got := envMap(StripEnv(in))
	if got["PROMPT_COMMAND"] != "history -a" || got["ZDOTDIR"] != "/mine" || got["NODE_OPTIONS"] != "--inspect" ||
		got["DYLD_INSERT_LIBRARIES"] != "/my/own.dylib" {
		t.Errorf("StripEnv changed foreign values: %v", got)
	}
}

func withGOOS(t *testing.T, os string) {
	t.Helper()
	orig := goos
	goos = os
	t.Cleanup(func() { goos = orig })
}

func TestPaneEnv_Linux(t *testing.T) {
	withGOOS(t, "linux")
	linuxShim := shim.Paths{Dir: "/s", Lib: "/s/libsaxvnet.so"} // no SIP helpers on Linux
	daemon := envMap(DaemonEnv(Member{Router: "v1", Host: "api", IP: "10.0.0.2", Subnet: "10.0.0.0/24", Dir: "/d"}, linuxShim))
	if _, ok := daemon[EnvReinject]; ok {
		t.Error("no Node reinject on Linux")
	}
	daemon["LD_PRELOAD"] = "/usr/lib/libmine.so"

	env := envMap(PaneEnv(func(k string) string { return daemon[k] }))
	if env["LD_PRELOAD"] != "/s/libsaxvnet.so:/usr/lib/libmine.so" {
		t.Errorf("LD_PRELOAD = %q (shim first, user's preload kept)", env["LD_PRELOAD"])
	}
	for _, k := range []string{"DYLD_INSERT_LIBRARIES", "NODE_OPTIONS", "ZDOTDIR", "PROMPT_COMMAND"} {
		if _, ok := env[k]; ok {
			t.Errorf("%s must not be set on Linux", k)
		}
	}

	// Stripping restores the user's own preload.
	var environ []string
	for k, v := range env {
		environ = append(environ, k+"="+v)
	}
	environ = append(environ, EnvShim+"=/s/libsaxvnet.so")
	if got := envMap(StripEnv(environ)); got["LD_PRELOAD"] != "/usr/lib/libmine.so" {
		t.Errorf("stripped LD_PRELOAD = %q", got["LD_PRELOAD"])
	}
}

func TestSupervisorEnvAndWrapper(t *testing.T) {
	withGOOS(t, "linux")
	p := shim.Paths{Dir: "/s", Lib: "/s/libsaxvnet.so", Supervisor: "/s/sax-vnet-sup"}
	daemon := envMap(DaemonEnv(Member{Router: "v1", Host: "api", IP: "10.0.0.2", Subnet: "10.0.0.0/24", Dir: "/d"}, p))
	if daemon[EnvSupervisor] != "/s/sax-vnet-sup" {
		t.Fatalf("daemon env = %v", daemon)
	}
	getenv := func(k string) string { return daemon[k] }

	if w := PaneWrapper(getenv); len(w) != 1 || w[0] != "/s/sax-vnet-sup" {
		t.Errorf("PaneWrapper = %v", w)
	}
	if _, ok := envMap(PaneEnv(getenv))["LD_PRELOAD"]; ok {
		t.Error("with a supervisor, LD_PRELOAD is the supervisor's fallback, not the pane's default")
	}

	// macOS and sessions outside a router get no wrapper.
	if w := PaneWrapper(func(string) string { return "" }); w != nil {
		t.Errorf("no router: %v", w)
	}
	withGOOS(t, "darwin")
	if w := PaneWrapper(getenv); w != nil {
		t.Errorf("darwin: %v", w)
	}
}

// macOS panes find the real python3 & co first on PATH (devbin), bash keeps
// it first at each prompt, and stripping restores the user's PATH.
func TestPaneEnv_DevBin(t *testing.T) {
	withGOOS(t, "darwin")
	p := testShim
	p.DevBin = "/s/devbin-abc"
	daemon := envMap(DaemonEnv(Member{Router: "v1", Host: "api", IP: "10.0.0.2", Subnet: "10.0.0.0/24", Dir: "/d"}, p))
	if daemon[EnvDevBin] != "/s/devbin-abc" {
		t.Fatalf("daemon env = %v", daemon)
	}
	daemon["PATH"] = "/usr/bin:/s/devbin-abc:/opt/bin"

	env := envMap(PaneEnv(func(k string) string { return daemon[k] }))
	if env["PATH"] != "/s/devbin-abc:/usr/bin:/opt/bin" {
		t.Errorf("PATH = %q, want devbin first, once", env["PATH"])
	}
	if !strings.Contains(env["PROMPT_COMMAND"], `PATH='/s/devbin-abc':"${PATH//'/s/devbin-abc:'/}"`) {
		t.Errorf("PROMPT_COMMAND = %q", env["PROMPT_COMMAND"])
	}

	merged := map[string]string{}
	for k, v := range daemon {
		merged[k] = v
	}
	for k, v := range env {
		merged[k] = v
	}
	var environ []string
	for k, v := range merged {
		environ = append(environ, k+"="+v)
	}
	got := envMap(StripEnv(environ))
	if got["PATH"] != "/usr/bin:/opt/bin" {
		t.Errorf("stripped PATH = %q", got["PATH"])
	}
	if _, ok := got["PROMPT_COMMAND"]; ok {
		t.Error("our PROMPT_COMMAND (with the PATH fix) not stripped")
	}

	withGOOS(t, "linux")
	if env := envMap(PaneEnv(func(k string) string { return daemon[k] })); env["PATH"] != "" {
		t.Errorf("no devbin PATH on Linux: %q", env["PATH"])
	}
}
