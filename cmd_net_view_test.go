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
)

func TestNetUp_AttachesUnlessDetached(t *testing.T) {
	fakeShim(t)
	s := vnet.NewStore(t.TempDir())
	run := newFakeRunner()
	if err := netUp(&bytes.Buffer{}, s, run, projectRef{File: writeEnv(t, upEnv)}, true); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(run.attached, []string{"v1"}) {
		t.Errorf("attached = %v", run.attached)
	}

	run2 := newFakeRunner()
	if err := netUp(&bytes.Buffer{}, vnet.NewStore(t.TempDir()), run2, projectRef{File: writeEnv(t, upEnv)}, false); err != nil {
		t.Fatal(err)
	}
	if len(run2.attached) != 0 || len(run2.tabs["v1"]) != 2 {
		t.Errorf("-d: attached %v, tabs %d", run2.attached, len(run2.tabs["v1"]))
	}
}

func TestNetUp_AddsNewAppsToRunningSession(t *testing.T) {
	fakeShim(t)
	s := vnet.NewStore(t.TempDir())
	run := newFakeRunner()
	small := strings.Replace(upEnv, "  web: { cmd: \"ng serve\" }\n", "", 1)
	small = strings.Replace(small, "  - bob.lan/*     -> web:4200\n", "", 1)
	if err := netUp(&bytes.Buffer{}, s, run, projectRef{File: writeEnv(t, small)}, false); err != nil {
		t.Fatal(err)
	}
	if len(run.tabs["v1"]) != 1 {
		t.Fatalf("tabs = %+v", run.tabs["v1"])
	}
	var out bytes.Buffer
	if err := netUp(&out, s, run, projectRef{File: writeEnv(t, upEnv)}, false); err != nil {
		t.Fatal(err)
	}
	if len(run.tabs["v1"]) != 2 || !strings.Contains(out.String(), "web  10.12.12.2  started") ||
		!strings.Contains(out.String(), "api  10.12.12.4  running") {
		t.Errorf("tabs %d\n%s", len(run.tabs["v1"]), out.String())
	}
}

func TestNetUp_NeverTakesOverForeignSession(t *testing.T) {
	fakeShim(t)
	run := newFakeRunner()
	run.alive["v1"] = true // the user's own session called v1
	err := netUp(&bytes.Buffer{}, vnet.NewStore(t.TempDir()), run, projectRef{File: writeEnv(t, upEnv)}, false)
	if err == nil || !strings.Contains(err.Error(), "not started by sax net up") {
		t.Errorf("err = %v", err)
	}
	if len(run.killed) != 0 || len(run.tabs["v1"]) != 0 {
		t.Errorf("touched a foreign session: killed %v tabs %v", run.killed, run.tabs["v1"])
	}
}

func TestNetUp_StopsLegacyPerAppSessions(t *testing.T) {
	fakeShim(t)
	s := vnet.NewStore(t.TempDir())
	r, _ := vnet.NewRouter("v1", "10.12.12.0/24", "lan")
	_ = s.Create(r)
	_, _ = s.Update("v1", func(r vnet.Router) (vnet.Router, error) { // as sax v1.6 left it
		r, _, _ = r.WithSession("api", "10.12.12.4")
		return r.WithOwner("api", "v1.api"), nil
	})
	run := newFakeRunner()
	run.alive["v1.api"] = true
	var out bytes.Buffer
	if err := netUp(&out, s, run, projectRef{File: writeEnv(t, upEnv)}, false); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(run.killed, []string{"v1.api"}) || !strings.Contains(out.String(), "now run as tabs") {
		t.Errorf("killed %v\n%s", run.killed, out.String())
	}
}

func TestNetDown_StopsView(t *testing.T) {
	fakeShim(t)
	s := vnet.NewStore(t.TempDir())
	run := newFakeRunner()
	if err := netUp(&bytes.Buffer{}, s, run, projectRef{File: writeEnv(t, upEnv)}, false); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := netDown(&out, s, run, "v1"); err != nil {
		t.Fatal(err)
	}
	if run.alive["v1"] || !strings.Contains(out.String(), "stopped view v1") {
		t.Errorf("session still alive (%v):\n%s", run.alive["v1"], out.String())
	}
	if r, _ := s.Get("v1"); r.View != "" || len(r.Sessions) != 0 {
		t.Errorf("router after down = %+v", r)
	}
}

func TestInitTabsEnvRoundTrip(t *testing.T) {
	tabs := []server.TabSpec{{Name: "api", Cmd: []string{"sax", "-a", "v1.api"}}}
	kv, err := initTabsEnv(tabs)
	if err != nil {
		t.Fatal(err)
	}
	k, v, _ := strings.Cut(kv, "=")
	t.Setenv(k, v)
	if got := initTabsFromEnv(); !reflect.DeepEqual(got, tabs) {
		t.Errorf("round trip = %+v", got)
	}
	if got := initTabsFromEnv(); got != nil {
		t.Error("the variable must be cleared after reading (panes must not inherit it)")
	}
}

func TestWantsAttach_DetachFlag(t *testing.T) {
	for _, args := range [][]string{{"-f", "x.yaml", "-d"}, {"--detach"}} {
		if wantsAttach(args) {
			t.Errorf("wantsAttach(%v) = true", args)
		}
	}
}

func TestNetUp_DefaultFileIsDotSaxLan(t *testing.T) {
	fakeShim(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, vnet.EnvFileName), []byte(upEnv), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	s := vnet.NewStore(t.TempDir())
	run := newFakeRunner()

	if err := netUp(&bytes.Buffer{}, s, run, projectRef{File: ""}, false); err != nil {
		t.Fatalf("up without -f: %v", err)
	}
	if len(run.tabs["v1"]) != 2 {
		t.Error(".sax.lan was not used")
	}
	var out bytes.Buffer
	if err := netCheck(&out, projectRef{File: ""}); err != nil || !strings.Contains(out.String(), ".sax.lan: ok") {
		t.Errorf("check without -f: %v\n%s", err, out.String())
	}

	t.Chdir(t.TempDir())
	err := netUp(&bytes.Buffer{}, s, run, projectRef{File: ""}, false)
	if err == nil || !strings.Contains(err.Error(), "no .sax.lan") || !strings.Contains(err.Error(), "-f") {
		t.Errorf("no file: err = %v", err)
	}
}

// A view started by sax v1.6 (nested clients) cannot add tabs: replace it.
func TestNetUp_ReplacesOldStyleView(t *testing.T) {
	fakeShim(t)
	s := vnet.NewStore(t.TempDir())
	r, _ := vnet.NewRouter("v1", "10.12.12.0/24", "lan")
	_ = s.Create(r)
	_, _ = s.Update("v1", func(r vnet.Router) (vnet.Router, error) { return r.WithView("v1"), nil })
	run := newFakeRunner()
	run.alive["v1"] = true
	run.addTabErr = errAddTabUnsupported
	if err := netUp(&bytes.Buffer{}, s, run, projectRef{File: writeEnv(t, upEnv)}, false); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(run.killed, []string{"v1"}) || len(run.tabs["v1"]) != 2 {
		t.Errorf("killed %v, tabs %+v", run.killed, run.tabs["v1"])
	}
}

// Adding an app must not move the others: their running tabs keep the IP
// they were started with.
func TestNetUp_ExistingAppsKeepTheirIPs(t *testing.T) {
	fakeShim(t)
	s := vnet.NewStore(t.TempDir())
	run := newFakeRunner()
	two := "router: v1\nsubnet: 10.12.12.0/24\nsessions:\n  api: {cmd: a}\n  web: {cmd: w}\n"
	if err := netUp(&bytes.Buffer{}, s, run, projectRef{File: writeEnv(t, two)}, false); err != nil {
		t.Fatal(err)
	}
	before, _ := s.Get("v1")

	three := two + "  db: {cmd: d}\n" // sorts between api and web
	if err := netUp(&bytes.Buffer{}, s, run, projectRef{File: writeEnv(t, three)}, false); err != nil {
		t.Fatal(err)
	}
	after, _ := s.Get("v1")
	for _, app := range []string{"api", "web"} {
		if after.Sessions[app] != before.Sessions[app] {
			t.Errorf("%s moved from %s to %s", app, before.Sessions[app], after.Sessions[app])
		}
	}
	if db := after.Sessions["db"]; db == "" || db == after.Sessions["api"] || db == after.Sessions["web"] {
		t.Errorf("db got %q", db)
	}
}

func TestBannerSummary(t *testing.T) {
	cases := map[string]vnet.Banner{
		"off":             {Off: true},
		"file /x.html":    {File: "/x.html"},
		"custom html":     {HTML: "<b>"},
		`text "{branch}"`: {Text: "{branch}"},
	}
	for want, b := range cases {
		if got := bannerSummary(b); got != want {
			t.Errorf("bannerSummary(%+v) = %q, want %q", b, got, want)
		}
	}
	if got := bannerSummary(vnet.Banner{}); !strings.HasPrefix(got, "default") {
		t.Errorf("default = %q", got)
	}
}
