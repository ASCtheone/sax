package vnet

import (
	"strings"
	"testing"
)

func TestParsePublish(t *testing.T) {
	p, err := ParsePublish(" 15432 ->  db:5432 ")
	if err != nil {
		t.Fatal(err)
	}
	if p.HostPort != 15432 || p.Target != (Endpoint{Host: "db", Port: 5432}) || p.String() != "15432 -> db:5432" {
		t.Errorf("got %+v / %q", p, p.String())
	}
	for _, bad := range []string{"15432", "x -> db:1", "0 -> db:1", "15432 -> db", "15432 -> b@d:1"} {
		if _, err := ParsePublish(bad); err == nil {
			t.Errorf("ParsePublish(%q) expected error", bad)
		}
	}
}

func TestWithPublish(t *testing.T) {
	r, err := mustRouter(t).WithPublish("15432 -> db:5432")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.WithPublish("15432 -> other:1"); err == nil || !strings.Contains(err.Error(), "15432") {
		t.Errorf("duplicate host port err = %v", err)
	}
	r2, err := r.WithoutPublish("15432")
	if err != nil || len(r2.Publish) != 0 || len(r.Publish) != 1 {
		t.Errorf("WithoutPublish: %v / %v / %v", r2.Publish, r.Publish, err)
	}
	if _, err := r2.WithoutPublish("15432"); err == nil {
		t.Error("removing a missing publish should fail")
	}
	if _, err := r2.WithoutPublish("abc"); err == nil {
		t.Error("bad port should fail")
	}
}

func TestPlan_Publish(t *testing.T) {
	e, err := ParseEnvFile([]byte("router: v1\nsubnet: 10.0.0.0/24\nsessions:\n  db: {cmd: x}\npublish:\n  - 15432 -> db:5432\n"))
	if err != nil {
		t.Fatal(err)
	}
	r, _, err := e.Plan()
	if err != nil || len(r.Publish) != 1 {
		t.Fatalf("plan publish = %v, %v", r.Publish, err)
	}
	e.Publish = []string{"15432 -> ghost:1"}
	if _, _, err := e.Plan(); err == nil {
		t.Error("publish to unknown session should fail the plan")
	}
}

func TestResolve(t *testing.T) {
	r, _, _ := mustRouter(t).WithSession("api", "10.12.12.4")
	if ip, ok := r.Resolve(Endpoint{Host: "api", Port: 1}); !ok || ip != "10.12.12.4" {
		t.Errorf("Resolve(api) = %s, %v", ip, ok)
	}
	if _, ok := r.Resolve(Endpoint{Host: "10.99.0.1", Port: 1}); ok {
		t.Error("address outside the subnet must not resolve")
	}
}
