package gateway

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/asc/sax/internal/vnet"
)

func TestHostDNS(t *testing.T) {
	h, err := newHostDNS("127.0.0.1:0", t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	defer h.close()
	reg, _ := vnet.Registration{}.With("lan")
	h.domains.Store(&reg)
	res := resolverFor(h.conn.LocalAddr().String())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	for _, name := range []string{"app1.lan", "app1.v2.lan", "anything.at.all.lan"} {
		ips, err := res.LookupIP(ctx, "ip", name)
		if err != nil || len(ips) != 1 || !ips[0].Equal(net.IPv4(127, 0, 0, 1)) {
			t.Errorf("LookupIP(%q) = %v, %v; want only 127.0.0.1 (no IPv6)", name, ips, err)
		}
	}
	if _, err := res.LookupHost(ctx, "example.com"); err == nil {
		t.Error("unregistered domains must be refused, never forwarded")
	}
}

// startGateway runs a gateway with registered-domain support on test ports.
func startGateway(t *testing.T, s vnet.Store) (*Gateway, *atomic.Bool) {
	t.Helper()
	var port80 atomic.Bool
	g := New(Config{
		Store: s, HostAddr: "127.0.0.1:0", Reload: 50 * time.Millisecond,
		HostDNSAddr: "127.0.0.1:0", Port80Addr: "127.0.0.1:0",
		OnPort80: func(on bool) { port80.Store(on) },
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- g.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	waitFor(t, func() bool { return g.HostAddr() != "" })
	return g, &port80
}

func addRouter(t *testing.T, s vnet.Store, name string) {
	t.Helper()
	r, _ := vnet.NewRouter(name, "10.12.12.0/24", "lan")
	if err := s.Create(r); err != nil {
		t.Fatal(err)
	}
	_, err := s.Update(name, func(r vnet.Router) (vnet.Router, error) {
		r, _, _ = r.WithSession("api", "10.12.12.4")
		return r.WithRule("bob.lan/* -> api:3200")
	})
	if err != nil {
		t.Fatal(err)
	}
	fakeSession(t, s, name, "10.12.12.4", 3200, "api-"+name)
}

func TestGateway_RegisteredDomains(t *testing.T) {
	s := vnet.NewStore(t.TempDir())
	addRouter(t, s, "v1")
	g, port80 := startGateway(t, s)

	// Not registered: plain names are not served, .localhost still is.
	if code, body := get(t, g.HostAddr(), "bob.lan", "/"); code != 404 || !strings.Contains(body, "sax net register") {
		t.Errorf("unregistered = %d %q", code, body)
	}
	if code, body := get(t, g.HostAddr(), "bob.lan.v1.localhost", "/x"); code != 200 || !strings.HasPrefix(body, "api-v1 /x") {
		t.Errorf("localhost fallback = %d %q", code, body)
	}
	if g.Port80Addr() != "" {
		t.Error(":80 must stay closed until a domain is registered")
	}

	reg, _ := vnet.Registration{}.With("lan")
	if err := s.SaveRegistration(reg); err != nil {
		t.Fatal(err)
	}
	waitFor(t, port80.Load)
	addr := g.Port80Addr()

	if code, body := get(t, addr, "bob.lan", "/api/users"); code != 200 || !strings.HasPrefix(body, "api-v1 /api/users") {
		t.Errorf("bob.lan = %d %q", code, body)
	}

	// A second router with the same name makes bob.lan ambiguous.
	addRouter(t, s, "v2")
	waitFor(t, func() bool {
		code, _ := get(t, addr, "bob.v2.lan", "/")
		return code == 200
	})
	code, body := get(t, addr, "bob.lan", "/")
	if code != 404 || !strings.Contains(body, "http://bob.v1.lan") || !strings.Contains(body, "http://bob.v2.lan") {
		t.Errorf("ambiguous = %d %q", code, body)
	}
	for _, r := range []string{"v1", "v2"} {
		host := fmt.Sprintf("bob.%s.lan", r)
		if code, body := get(t, addr, host, "/p"); code != 200 || !strings.HasPrefix(body, "api-"+r+" /p") {
			t.Errorf("%s = %d %q", host, code, body)
		}
	}
	if code, body := get(t, addr, "nope.lan", "/"); code != 404 || !strings.Contains(body, "no route") {
		t.Errorf("unknown = %d %q", code, body)
	}

	// The host DNS answers registered names with 127.0.0.1.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if ips, err := resolverFor(g.HostDNSAddr()).LookupHost(ctx, "bob.v2.lan"); err != nil || ips[0] != "127.0.0.1" {
		t.Errorf("host dns = %v, %v", ips, err)
	}

	// Unregistering closes :80 again.
	if err := s.SaveRegistration(reg.Without("lan")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !port80.Load() })
}
