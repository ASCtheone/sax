package main

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/asc/sax/internal/server"
	"github.com/asc/sax/internal/vnet"
)

func TestNetUp_OpensViewWithTabPerApp(t *testing.T) {
	fakeShim(t)
	s := vnet.NewStore(t.TempDir())
	run := newFakeRunner()
	var out bytes.Buffer
	if err := netUp(&out, s, run, writeEnv(t, upEnv), true); err != nil {
		t.Fatal(err)
	}

	tabs := run.tabs["v1"]
	if len(tabs) != 2 || tabs[0].Name != "api" || tabs[1].Name != "web" {
		t.Fatalf("view tabs = %+v", tabs)
	}
	if got := tabs[0].Cmd[1:]; !reflect.DeepEqual(got, []string{"-a", "v1.api"}) {
		t.Errorf("api tab runs %v, want a sax client attached to v1.api", got)
	}
	if !reflect.DeepEqual(run.attached, []string{"v1"}) {
		t.Errorf("attached = %v, want the view", run.attached)
	}
	if r, _ := s.Get("v1"); r.View != "v1" {
		t.Errorf("router view = %q", r.View)
	}
	if !strings.Contains(out.String(), "sax -a v1") {
		t.Errorf("output should say how to reattach:\n%s", out.String())
	}
}

func TestNetUp_DetachedDoesNotAttach(t *testing.T) {
	fakeShim(t)
	s := vnet.NewStore(t.TempDir())
	run := newFakeRunner()
	if err := netUp(&bytes.Buffer{}, s, run, writeEnv(t, upEnv), false); err != nil {
		t.Fatal(err)
	}
	if len(run.attached) != 0 {
		t.Errorf("-d attached anyway: %v", run.attached)
	}
	if _, ok := run.tabs["v1"]; !ok {
		t.Error("the view should still exist for sax -a v1 later")
	}
}

func TestNetUp_RebuildsViewButNeverForeignSessions(t *testing.T) {
	fakeShim(t)
	s := vnet.NewStore(t.TempDir())
	run := newFakeRunner()
	path := writeEnv(t, upEnv)
	if err := netUp(&bytes.Buffer{}, s, run, path, false); err != nil {
		t.Fatal(err)
	}
	// Second up: the old view is replaced so its tabs match the apps.
	if err := netUp(&bytes.Buffer{}, s, run, path, false); err != nil {
		t.Fatal(err)
	}
	if len(run.killed) != 1 || run.killed[0] != "v1" {
		t.Errorf("killed = %v, want the old view only", run.killed)
	}

	// A user session that happens to be called like the router is left alone.
	s2 := vnet.NewStore(t.TempDir())
	run2 := newFakeRunner()
	run2.alive["v1"] = true
	err := netUp(&bytes.Buffer{}, s2, run2, path, false)
	if err == nil || !strings.Contains(err.Error(), "not this router's view") {
		t.Errorf("err = %v", err)
	}
	if len(run2.killed) != 0 {
		t.Errorf("killed a foreign session: %v", run2.killed)
	}
}

func TestNetDown_StopsView(t *testing.T) {
	fakeShim(t)
	s := vnet.NewStore(t.TempDir())
	run := newFakeRunner()
	if err := netUp(&bytes.Buffer{}, s, run, writeEnv(t, upEnv), false); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := netDown(&out, s, run, "v1"); err != nil {
		t.Fatal(err)
	}
	if run.alive["v1"] || !strings.Contains(out.String(), "stopped view v1") {
		t.Errorf("view still alive (%v):\n%s", run.alive["v1"], out.String())
	}
	if r, _ := s.Get("v1"); r.View != "" {
		t.Errorf("view not cleared: %q", r.View)
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
