package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/asc/sax/internal/server"
	"github.com/asc/sax/internal/vnet"
	"github.com/asc/sax/internal/vnet/shim"
)

type launched struct {
	command []string
	workDir string
	env     []string
}

type fakeRunner struct {
	addTabErr error
	alive     map[string]bool
	launched  map[string]launched
	tabs      map[string][]server.TabSpec
	attached  []string
	killed    []string
}

func newFakeRunner() *fakeRunner {
	return &fakeRunner{alive: map[string]bool{}, launched: map[string]launched{}, tabs: map[string][]server.TabSpec{}}
}

func (f *fakeRunner) Alive(name string) bool { return f.alive[name] }
func (f *fakeRunner) Launch(name string, command []string, workDir string, env []string) error {
	f.launched[name] = launched{command, workDir, env}
	f.alive[name] = true
	return nil
}
func (f *fakeRunner) LaunchTabs(name string, tabs []server.TabSpec) error {
	f.tabs[name] = tabs
	f.alive[name] = true
	return nil
}
func (f *fakeRunner) AddTab(name string, tab server.TabSpec) (bool, error) {
	if f.addTabErr != nil {
		return false, f.addTabErr
	}
	for _, t := range f.tabs[name] {
		if t.Name == tab.Name {
			return true, nil
		}
	}
	f.tabs[name] = append(f.tabs[name], tab)
	return false, nil
}
func (f *fakeRunner) Attach(name string) { f.attached = append(f.attached, name) }
func (f *fakeRunner) Kill(name string) {
	f.killed = append(f.killed, name)
	delete(f.alive, name)
}

func fakeShim(t *testing.T) {
	t.Helper()
	orig := installShim
	installShim = func() (shim.Paths, error) {
		return shim.Paths{Dir: "/s", Lib: "/s/lib.dylib", Reinject: "/s/re.cjs", ZDotDir: "/s/z"}, nil
	}
	t.Cleanup(func() { installShim = orig })
}

func envOf(l launched) map[string]string {
	m := map[string]string{}
	for _, kv := range l.env {
		k, v, _ := strings.Cut(kv, "=")
		m[k] = v
	}
	return m
}

const upEnv = `router: v1
subnet: 10.12.12.0/24
domain: lan
sessions:
  api: { ip: 10.12.12.4, cwd: ./api, cmd: "npm run start:dev", env: { NODE_ENV: development } }
  web: { cmd: "ng serve" }
routes:
  - bob.lan/api/* -> api:3200
  - bob.lan/*     -> web:4200
`

func writeEnv(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "v1.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// tabOf returns the spec of an app tab in the router's session.
func tabOf(t *testing.T, run *fakeRunner, session, app string) server.TabSpec {
	t.Helper()
	for _, tab := range run.tabs[session] {
		if tab.Name == app {
			return tab
		}
	}
	t.Fatalf("no tab %q in session %q (tabs: %+v)", app, session, run.tabs[session])
	return server.TabSpec{}
}

func tabEnv(tab server.TabSpec) map[string]string {
	m := map[string]string{}
	for _, kv := range tab.Env {
		k, v, _ := strings.Cut(kv, "=")
		m[k] = v
	}
	return m
}

func TestNetUp_RunsAppsAsTabsWithNetworkEnv(t *testing.T) {
	fakeShim(t)
	t.Setenv("SHELL", "/bin/zsh")
	s := vnet.NewStore(t.TempDir())
	run := newFakeRunner()
	path := writeEnv(t, upEnv)
	var out bytes.Buffer

	if err := netUp(&out, s, run, path, false); err != nil {
		t.Fatal(err)
	}
	if len(run.launched) != 0 && run.launched["v1.api"].command != nil {
		t.Errorf("apps must not get their own sessions anymore: %v", run.launched)
	}

	api := tabOf(t, run, "v1", "api")
	env := tabEnv(api)
	if env[vnet.EnvIP] != "10.12.12.4" || env[vnet.EnvHost] != "api" || env[vnet.EnvShim] != "/s/lib.dylib" {
		t.Errorf("api env = %v", env)
	}
	if env["NODE_ENV"] != "development" {
		t.Error("session env from the file not passed")
	}
	if api.Dir != filepath.Join(filepath.Dir(path), "api") {
		t.Errorf("dir = %s", api.Dir)
	}
	if api.Input != "npm run start:dev" || api.Cmd[len(api.Cmd)-1] != "/bin/zsh" {
		t.Errorf("tab = cmd %q input %q; want an interactive shell with the app command typed in", api.Cmd, api.Input)
	}

	r, _ := s.Get("v1")
	if r.Sessions["web"] != "10.12.12.2" || r.Owners["web"] != "v1" || r.View != "v1" || len(r.Rules) != 2 {
		t.Errorf("router after up = %+v", r)
	}
	if !strings.Contains(out.String(), "api  10.12.12.4  started") || !strings.Contains(out.String(), "sax -a v1") {
		t.Errorf("output:\n%s", out.String())
	}

	// Second up: the session is running, so apps are kept, not restarted.
	out.Reset()
	if err := netUp(&out, s, run, path, false); err != nil {
		t.Fatal(err)
	}
	if len(run.tabs["v1"]) != 2 || !strings.Contains(out.String(), "api  10.12.12.4  running") {
		t.Errorf("second up: tabs %d\n%s", len(run.tabs["v1"]), out.String())
	}
	if len(run.killed) != 0 {
		t.Errorf("second up killed %v", run.killed)
	}
}

func TestNetUp_TwoVersionsSideBySide(t *testing.T) {
	fakeShim(t)
	s := vnet.NewStore(t.TempDir())
	run := newFakeRunner()
	var out bytes.Buffer
	for _, v := range []string{"v1", "v2"} {
		if err := netUp(&out, s, run, writeEnv(t, strings.Replace(upEnv, "router: v1", "router: "+v, 1)), false); err != nil {
			t.Fatal(err)
		}
	}
	a1, a2 := tabEnv(tabOf(t, run, "v1", "api")), tabEnv(tabOf(t, run, "v2", "api"))
	if a1[vnet.EnvIP] != a2[vnet.EnvIP] || a1[vnet.EnvDir] == a2[vnet.EnvDir] {
		t.Errorf("same IP expected in separate routers: %v vs %v", a1, a2)
	}
}

func TestNetUp_SubnetMismatch(t *testing.T) {
	fakeShim(t)
	s := vnet.NewStore(t.TempDir())
	r, _ := vnet.NewRouter("v1", "10.99.0.0/24", "")
	_ = s.Create(r)
	err := netUp(&bytes.Buffer{}, s, newFakeRunner(), writeEnv(t, upEnv), false)
	if err == nil || !strings.Contains(err.Error(), "already exists with subnet") {
		t.Errorf("err = %v", err)
	}
}

func TestNetUp_KeepsHandJoinedSessions(t *testing.T) {
	fakeShim(t)
	s := vnet.NewStore(t.TempDir())
	r, _ := vnet.NewRouter("v1", "10.12.12.0/24", "lan")
	_ = s.Create(r)
	if _, _, err := netJoin(s, "debug", "v1:tool", "10.12.12.50"); err != nil {
		t.Fatal(err)
	}
	if err := netUp(&bytes.Buffer{}, s, newFakeRunner(), writeEnv(t, upEnv), false); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get("v1")
	if got.Sessions["tool"] != "10.12.12.50" || got.Owners["tool"] != "debug" {
		t.Errorf("hand-joined session lost: %+v", got)
	}
}

func TestNetDownAndRemove(t *testing.T) {
	fakeShim(t)
	s := vnet.NewStore(t.TempDir())
	run := newFakeRunner()
	var out bytes.Buffer
	path := writeEnv(t, upEnv)
	if err := netUp(&out, s, run, path, false); err != nil {
		t.Fatal(err)
	}

	if err := netRemove(&out, s, run, []string{"v1"}); err == nil || !strings.Contains(err.Error(), "net down") {
		t.Errorf("rm with live sessions err = %v", err)
	}

	router, err := routerFromArgs(nil, path)
	if err != nil || router != "v1" {
		t.Fatalf("routerFromArgs = %q, %v", router, err)
	}
	if err := netDown(&out, s, run, router); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(run.killed, []string{"v1"}) {
		t.Errorf("killed = %v, want the router's session (its tabs are the apps)", run.killed)
	}
	r, _ := s.Get("v1")
	if len(r.Sessions) != 0 || len(r.Rules) != 2 {
		t.Errorf("after down: sessions %v rules %v (rules should stay)", r.Sessions, r.Rules)
	}
	if err := netRemove(&out, s, run, []string{"v1"}); err != nil {
		t.Errorf("rm after down: %v", err)
	}
	if err := netDown(&out, s, run, ""); err == nil {
		t.Error("down without router should fail")
	}
}

func TestNetJoin(t *testing.T) {
	fakeShim(t)
	s := vnet.NewStore(t.TempDir())
	r, _ := vnet.NewRouter("v1", "10.12.12.0/24", "lan")
	_ = s.Create(r)

	env, m, err := netJoin(s, "v1.api", "v1", "")
	if err != nil {
		t.Fatal(err)
	}
	if m.Host != "api" || m.IP != "10.12.12.2" || len(env) == 0 {
		t.Errorf("member = %+v", m)
	}
	// Same sax session again: same address.
	if _, m2, _ := netJoin(s, "v1.api", "v1", ""); m2.IP != m.IP {
		t.Errorf("rejoin moved %s -> %s", m.IP, m2.IP)
	}
	// Another sax session cannot take the same host name.
	if _, _, err := netJoin(s, "other", "v1:api", ""); err == nil || !strings.Contains(err.Error(), "already used") {
		t.Errorf("err = %v", err)
	}
	if _, _, err := netJoin(s, "x", "ghost", ""); err == nil || !strings.Contains(err.Error(), "sax net create ghost") {
		t.Errorf("missing router err = %v", err)
	}
	if _, _, err := netJoin(s, "bad_name", "v1", ""); err == nil {
		t.Error("underscore session name cannot become a host name")
	}
}

func TestSupervisionWarning(t *testing.T) {
	orig := supervisionAvailable
	defer func() { supervisionAvailable = orig }()

	withSup := shim.Paths{Lib: "/s/l.so", Supervisor: "/s/sax-vnet-sup"}
	supervisionAvailable = func(shim.Paths) bool { return true }
	if w := supervisionWarning(withSup); w != "" {
		t.Errorf("supported: %q", w)
	}
	supervisionAvailable = func(shim.Paths) bool { return false }
	if w := supervisionWarning(withSup); !strings.Contains(w, "LD_PRELOAD") {
		t.Errorf("blocked: %q", w)
	}
	if w := supervisionWarning(shim.Paths{Lib: "/s/l.dylib"}); w != "" {
		t.Errorf("macOS (no supervisor): %q", w)
	}
}
