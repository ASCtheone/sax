package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/asc/sax/internal/vnet"
	"github.com/asc/sax/internal/vnet/gateway"
	"github.com/asc/sax/internal/vnet/hostdns"
)

// fakeHost swaps the privileged runner, resolver checks and OS for a test.
type fakeHost struct {
	calls    [][]string
	resolves bool
	dnsUp    bool
	failSudo error
}

func installFakeHost(t *testing.T, goos string) *fakeHost {
	t.Helper()
	f := &fakeHost{resolves: true, dnsUp: true}
	origRun, origRes, origDNS, origOS := runPrivileged, systemResolves, hostDNSAnswers, hostOS
	runPrivileged = func(args []string, _ string) error {
		f.calls = append(f.calls, args)
		return f.failSudo
	}
	systemResolves = func(string) bool { return f.resolves }
	hostDNSAnswers = func(string) bool { return f.dnsUp }
	hostOS = goos
	t.Setenv("HOME", t.TempDir()) // isolate the gateway state file
	t.Cleanup(func() {
		runPrivileged, systemResolves, hostDNSAnswers, hostOS = origRun, origRes, origDNS, origOS
	})
	return f
}

func TestNetRegister_Darwin(t *testing.T) {
	f := installFakeHost(t, "darwin")
	s := vnet.NewStore(t.TempDir())
	run := newFakeRunner()
	var out bytes.Buffer

	if err := netRegister(&out, s, run, nil); err != nil {
		t.Fatal(err)
	}
	reg, _ := s.Registration()
	if !reflect.DeepEqual(reg.Domains, []string{"lan"}) {
		t.Errorf("registration = %v", reg.Domains)
	}
	wantWrite := []string{"sudo", "tee", "/etc/resolver/lan"}
	found := false
	for _, c := range f.calls {
		if reflect.DeepEqual(c, wantWrite) {
			found = true
		}
	}
	if !found {
		t.Errorf("sudo calls = %v, want %v", f.calls, wantWrite)
	}
	if _, ok := run.launched[gateway.SessionName]; !ok {
		t.Error("register should start the gateway (it answers .lan lookups)")
	}
	if !strings.Contains(out.String(), "write /etc/resolver/lan") || !strings.Contains(out.String(), "http://app1.lan") {
		t.Errorf("output:\n%s", out.String())
	}

	// Unregister removes the resolver file and the registration.
	f.calls = nil
	if err := netUnregister(&out, s, nil); err != nil {
		t.Fatal(err)
	}
	if reg, _ := s.Registration(); len(reg.Domains) != 0 {
		t.Errorf("after unregister = %v", reg.Domains)
	}
	if len(f.calls) == 0 || !reflect.DeepEqual(f.calls[0], []string{"sudo", "rm", "-f", "/etc/resolver/lan"}) {
		t.Errorf("unregister calls = %v", f.calls)
	}
}

func TestNetRegister_SudoRefusedSavesNothing(t *testing.T) {
	f := installFakeHost(t, "darwin")
	f.failSudo = errors.New("sudo: 1 incorrect password attempt")
	s := vnet.NewStore(t.TempDir())
	if err := netRegister(&bytes.Buffer{}, s, newFakeRunner(), []string{"test"}); err == nil {
		t.Fatal("expected the sudo failure")
	}
	if reg, _ := s.Registration(); len(reg.Domains) != 0 {
		t.Errorf("registration saved despite failure: %v", reg.Domains)
	}
}

func TestNetRegister_Unsupported(t *testing.T) {
	installFakeHost(t, "windows")
	err := netRegister(&bytes.Buffer{}, vnet.NewStore(t.TempDir()), newFakeRunner(), nil)
	if !errors.Is(err, hostdns.ErrUnsupported) || !strings.Contains(err.Error(), ".localhost") {
		t.Errorf("err = %v (should point to the .localhost fallback)", err)
	}
}

func TestNetRegister_RestartsOutdatedGateway(t *testing.T) {
	f := installFakeHost(t, "darwin")
	f.dnsUp = false // a running gateway from before host DNS existed
	s := vnet.NewStore(t.TempDir())
	run := newFakeRunner()
	run.alive[gateway.SessionName] = true
	if err := netRegister(&bytes.Buffer{}, s, run, nil); err != nil {
		t.Fatal(err)
	}
	if len(run.killed) != 1 || run.killed[0] != gateway.SessionName {
		t.Errorf("killed = %v", run.killed)
	}
	if _, ok := run.launched[gateway.SessionName]; !ok {
		t.Error("gateway not relaunched")
	}
}

func TestNetUnregister_NothingRegistered(t *testing.T) {
	installFakeHost(t, "darwin")
	var out bytes.Buffer
	if err := netUnregister(&out, vnet.NewStore(t.TempDir()), nil); err != nil || !strings.Contains(out.String(), "no domains") {
		t.Errorf("out = %q, err = %v", out.String(), err)
	}
}

func TestHostURL_RegisteredAndFallback(t *testing.T) {
	installFakeHost(t, "darwin")
	s := vnet.NewStore(t.TempDir())
	for _, name := range []string{"v1", "v2"} {
		r, _ := vnet.NewRouter(name, "10.12.12.0/24", "")
		_ = s.Create(r)
		_, _ = s.Update(name, func(r vnet.Router) (vnet.Router, error) {
			r, _ = r.WithRule("app1.lan/* -> web:4200")
			return r.WithRule("only" + name + ".lan/* -> web:4200")
		})
	}
	v1, _ := s.Get("v1")

	// Not registered: .localhost form with the gateway port.
	if got := hostURL(s, vnet.Registration{}, v1, "app1.lan"); got != "http://app1.lan.v1.localhost:7480" {
		t.Errorf("fallback = %s", got)
	}

	reg, _ := vnet.Registration{}.With("lan")
	if got := hostURL(s, reg, v1, "onlyv1.lan"); got != "http://onlyv1.lan:7480" {
		t.Errorf("unique, no :80 = %s", got)
	}
	if got := hostURL(s, reg, v1, "app1.lan"); got != "http://app1.v1.lan:7480" {
		t.Errorf("shared = %s", got)
	}

	// With the gateway on :80 the port disappears.
	_ = os.MkdirAll(vnetRoot(), 0o755)
	_ = os.WriteFile(filepath.Join(vnetRoot(), gatewayStateFile), []byte(`{"pid":1,"addr":"127.0.0.1:7480","port80":true}`), 0o644)
	if got := hostURL(s, reg, v1, "onlyv1.lan"); got != "http://onlyv1.lan" {
		t.Errorf("unique with :80 = %s", got)
	}
	if st := registrationStatus(s); !strings.Contains(st, "run `sax net register`") {
		t.Errorf("status without registration = %q", st)
	}
	_ = s.SaveRegistration(reg)
	if st := registrationStatus(s); !strings.Contains(st, ".lan registered") {
		t.Errorf("status = %q", st)
	}
}
