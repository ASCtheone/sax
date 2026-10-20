package vnet

import (
	"reflect"
	"testing"
)

func TestRegistration_WithWithout(t *testing.T) {
	var r Registration
	r2, err := r.With("LAN", "test", "lan")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(r2.Domains, []string{"lan", "test"}) {
		t.Errorf("With = %v (sorted, deduped, lowercase)", r2.Domains)
	}
	if len(r.Domains) != 0 {
		t.Error("With mutated its receiver")
	}
	for _, bad := range []string{"localhost", "bad_domain", "", "*.lan"} {
		if _, err := r.With(bad); err == nil {
			t.Errorf("With(%q) should fail", bad)
		}
	}
	if r3 := r2.Without("lan", "nope"); !reflect.DeepEqual(r3.Domains, []string{"test"}) {
		t.Errorf("Without = %v", r3.Domains)
	}
}

func TestRegistration_Covers(t *testing.T) {
	r, _ := Registration{}.With("lan", "dev.test")
	cases := map[string]string{
		"app1.lan":       "lan",
		"APP1.LAN.":      "lan",
		"app1.v2.lan:80": "lan",
		"api.dev.test":   "dev.test",
		"lan":            "",
		"app1.localhost": "",
		"app1.lan.evil":  "",
		"app1.dev.testx": "",
	}
	for host, want := range cases {
		got, ok := r.Covers(host)
		if got != want || ok != (want != "") {
			t.Errorf("Covers(%q) = %q, %v; want %q", host, got, ok, want)
		}
	}
}

func TestStore_Registration(t *testing.T) {
	s := NewStore(t.TempDir())
	r, err := s.Registration()
	if err != nil || len(r.Domains) != 0 {
		t.Fatalf("empty store: %v, %v", r, err)
	}
	r, _ = r.With("lan")
	if err := s.SaveRegistration(r); err != nil {
		t.Fatal(err)
	}
	got, err := s.Registration()
	if err != nil || !reflect.DeepEqual(got.Domains, []string{"lan"}) {
		t.Errorf("round trip = %v, %v", got, err)
	}
	// The registration file must not show up as a router.
	if routers, _ := s.List(); len(routers) != 0 {
		t.Errorf("List = %v", routers)
	}
}
