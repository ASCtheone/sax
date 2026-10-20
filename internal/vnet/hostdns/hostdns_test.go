package hostdns

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestRegisterPlan_Darwin(t *testing.T) {
	p, err := RegisterPlan(Env{GOOS: "darwin"}, []string{"lan", "test"}, "127.0.0.1:7453")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Files) != 2 || p.Files[0].Path != "/etc/resolver/lan" || p.Files[1].Path != "/etc/resolver/test" {
		t.Fatalf("files = %+v", p.Files)
	}
	c := p.Files[0].Content
	for _, want := range []string{"nameserver 127.0.0.1\n", "port 7453\n", "sax"} {
		if !strings.Contains(c, want) {
			t.Errorf("resolver file missing %q:\n%s", want, c)
		}
	}
	if len(p.After) == 0 || p.After[0][0] != "killall" {
		t.Errorf("after = %v (mDNSResponder should reload)", p.After)
	}
}

func TestUnregisterPlan_Darwin(t *testing.T) {
	p, err := UnregisterPlan(Env{GOOS: "darwin"}, []string{"lan"}, nil, "127.0.0.1:7453")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.Remove, []string{"/etc/resolver/lan"}) || len(p.Files) != 0 {
		t.Errorf("plan = %+v", p)
	}
}

func TestPlans_Linux(t *testing.T) {
	dir := t.TempDir()
	stub := filepath.Join(dir, "stub-resolv.conf")
	_ = os.WriteFile(stub, []byte("nameserver 127.0.0.53\n"), 0o644)
	env := Env{GOOS: "linux", ResolvedStub: stub}

	p, err := RegisterPlan(env, []string{"lan", "test"}, "127.0.0.1:7453")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Files) != 1 || p.Files[0].Path != resolvedDropIn {
		t.Fatalf("files = %+v", p.Files)
	}
	if c := p.Files[0].Content; !strings.Contains(c, "DNS=127.0.0.1:7453\n") || !strings.Contains(c, "Domains=~lan ~test\n") {
		t.Errorf("drop-in:\n%s", c)
	}
	if !reflect.DeepEqual(p.After, [][]string{{"systemctl", "restart", "systemd-resolved"}}) {
		t.Errorf("after = %v", p.After)
	}

	// Unregistering one of two domains rewrites the drop-in; the last one removes it.
	p, _ = UnregisterPlan(env, []string{"lan"}, []string{"test"}, "127.0.0.1:7453")
	if len(p.Files) != 1 || !strings.Contains(p.Files[0].Content, "Domains=~test\n") {
		t.Errorf("partial unregister = %+v", p)
	}
	p, _ = UnregisterPlan(env, []string{"test"}, nil, "127.0.0.1:7453")
	if !reflect.DeepEqual(p.Remove, []string{resolvedDropIn}) {
		t.Errorf("full unregister = %+v", p)
	}

	if _, err := RegisterPlan(Env{GOOS: "linux", ResolvedStub: filepath.Join(dir, "none")}, []string{"lan"}, "127.0.0.1:7453"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("no systemd-resolved: err = %v", err)
	}
}

func TestRegisterPlan_Unsupported(t *testing.T) {
	if _, err := RegisterPlan(Env{GOOS: "windows"}, []string{"lan"}, "127.0.0.1:7453"); !errors.Is(err, ErrUnsupported) {
		t.Errorf("windows: err = %v", err)
	}
	if _, err := RegisterPlan(Env{GOOS: "darwin"}, []string{"lan"}, "not-an-addr"); err == nil {
		t.Error("bad DNS address should fail")
	}
}

type call struct {
	args  []string
	stdin string
}

func TestApply(t *testing.T) {
	p := Plan{
		Files:  []File{{Path: "/etc/resolver/lan", Content: "x\n"}},
		Remove: []string{"/etc/resolver/old"},
		After:  [][]string{{"killall", "-HUP", "mDNSResponder"}},
	}
	var calls []call
	run := func(args []string, stdin string) error {
		calls = append(calls, call{args, stdin})
		return nil
	}
	if err := Apply(p, run); err != nil {
		t.Fatal(err)
	}
	want := []call{
		{[]string{"sudo", "mkdir", "-p", "/etc/resolver"}, ""},
		{[]string{"sudo", "tee", "/etc/resolver/lan"}, "x\n"},
		{[]string{"sudo", "rm", "-f", "/etc/resolver/old"}, ""},
		{[]string{"sudo", "killall", "-HUP", "mDNSResponder"}, ""},
	}
	if !reflect.DeepEqual(calls, want) {
		t.Errorf("calls =\n%v\nwant\n%v", calls, want)
	}

	boom := errors.New("sudo: a password is required")
	failing := func(args []string, _ string) error {
		if args[1] == "tee" {
			return boom
		}
		return nil
	}
	if err := Apply(p, failing); !errors.Is(err, boom) {
		t.Errorf("err = %v", err)
	}
	// A failing reload command is reported but does not fail the change.
	reloadFails := func(args []string, _ string) error {
		if args[1] == "killall" {
			return boom
		}
		return nil
	}
	if err := Apply(p, reloadFails); err != nil {
		t.Errorf("reload failure should be tolerated: %v", err)
	}
}
