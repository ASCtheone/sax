package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/asc/sax/internal/vnet"
	"github.com/asc/sax/internal/vnet/gateway"
)

func TestEnsureGateway(t *testing.T) {
	s := vnet.NewStore(t.TempDir())
	run := newFakeRunner()
	var out bytes.Buffer
	_ = netCreate(&out, s, []string{"v1"}, "10.12.12.0/24", "")

	if err := ensureGateway(&out, s, run); err != nil || len(run.launched) != 0 {
		t.Fatalf("no HTTP rules: gateway should not start (%v, %v)", run.launched, err)
	}

	_ = netMap(&out, s, []string{"v1", "bob.lan/*", "web:4200"})
	if err := ensureGateway(&out, s, run); err != nil {
		t.Fatal(err)
	}
	gw, ok := run.launched[gateway.SessionName]
	if !ok || len(gw.command) != 3 || gw.command[1] != "net" || gw.command[2] != "gateway" {
		t.Fatalf("gateway launch = %+v", gw)
	}

	run.launched = map[string]launched{}
	if err := ensureGateway(&out, s, run); err != nil || len(run.launched) != 0 {
		t.Error("running gateway must not be relaunched")
	}
}

func TestNetPublishFlow(t *testing.T) {
	s := vnet.NewStore(t.TempDir())
	run := newFakeRunner()
	var out bytes.Buffer
	_ = netCreate(&out, s, []string{"v1"}, "10.12.12.0/24", "")

	if err := netPublish(&out, s, run, []string{"v1", "15432", "db:5432"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := run.launched[gateway.SessionName]; !ok {
		t.Error("publish should start the gateway")
	}
	if err := netPublish(&out, s, run, []string{"v1", "15432", "->", "db:5432"}); err == nil {
		t.Error("duplicate host port should fail")
	}
	out.Reset()
	r, _ := s.Get("v1")
	printHostURLs(&out, s, r)
	if !strings.Contains(out.String(), "127.0.0.1:15432 -> db:5432") {
		t.Errorf("host URLs:\n%s", out.String())
	}
	if err := netUnpublish(&out, s, []string{"v1", "15432"}); err != nil {
		t.Fatal(err)
	}
	if err := netPublish(&out, s, run, []string{"v1"}); err == nil {
		t.Error("missing args should fail")
	}
}

func TestPrintHostURLs(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // no running gateway state: default port
	s := vnet.NewStore(t.TempDir())
	r, _ := vnet.NewRouter("v1", "10.12.12.0/24", "")
	r, _ = r.WithRule("bob.lan/* -> web:4200")
	var out bytes.Buffer
	printHostURLs(&out, s, r)
	if !strings.Contains(out.String(), "http://bob.lan.v1.localhost:7480") {
		t.Errorf("output:\n%s", out.String())
	}

	// A gateway on :80 drops the port from URLs.
	if err := os.MkdirAll(vnetRoot(), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(vnetRoot(), gatewayStateFile), []byte(`{"pid":1,"addr":"0.0.0.0:80"}`), 0o644)
	out.Reset()
	printHostURLs(&out, s, r)
	if !strings.Contains(out.String(), "http://bob.lan.v1.localhost\n") {
		t.Errorf("port 80 output:\n%s", out.String())
	}
}

func TestNetUp_PublishAndGateway(t *testing.T) {
	fakeShim(t)
	s := vnet.NewStore(t.TempDir())
	run := newFakeRunner()
	env := upEnv + "publish:\n  - 13200 -> api:3200\n"
	var out bytes.Buffer
	if err := netUp(&out, s, run, writeEnv(t, env)); err != nil {
		t.Fatal(err)
	}
	r, _ := s.Get("v1")
	if len(r.Publish) != 1 {
		t.Errorf("publish not applied: %v", r.Publish)
	}
	if _, ok := run.launched[gateway.SessionName]; !ok {
		t.Error("up with HTTP routes should start the gateway")
	}
	if !strings.Contains(out.String(), "http://bob.lan.v1.localhost") {
		t.Errorf("up should print host URLs:\n%s", out.String())
	}
}

func TestNeedsGateway_LinuxSessionsNeedDNS(t *testing.T) {
	s := vnet.NewStore(t.TempDir())
	r, _ := vnet.NewRouter("v1", "10.12.12.0/24", "")
	_ = s.Create(r)
	_, _ = s.Update("v1", func(r vnet.Router) (vnet.Router, error) {
		r, _, err := r.WithSession("api", "")
		return r, err
	})
	orig := hostOS
	defer func() { hostOS = orig }()

	hostOS = "darwin"
	if needsGateway(s) {
		t.Error("macOS sessions resolve names in the shim; no gateway needed")
	}
	hostOS = "linux"
	if !needsGateway(s) {
		t.Error("Linux sessions need the gateway's DNS stub")
	}
}
