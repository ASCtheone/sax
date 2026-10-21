package gateway

import (
	"context"
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
	addRouterApp(t, s, name, true)
}

// addRouterApp adds router name with bob.lan/* -> api:3200, and api
// listening when listening is set (a router that is down keeps its routes).
func addRouterApp(t *testing.T, s vnet.Store, name string, listening bool) {
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
	if listening {
		fakeSession(t, s, name, "10.12.12.4", 3200, "api-"+name)
	}
}

func TestGateway_RegisteredDomains(t *testing.T) {
	s := vnet.NewStore(t.TempDir())
	addRouter(t, s, "v1")
	g, port80 := startGateway(t, s)

	// Not registered: the gateway port serves the name and <name>.localhost.
	for _, host := range []string{"bob.lan", "bob.lan.localhost"} {
		if code, body := get(t, g.HostAddr(), host, "/x"); code != 200 || !strings.HasPrefix(body, "api-v1 /x") {
			t.Errorf("%s = %d %q", host, code, body)
		}
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

	// Another router with bob.lan whose app is not running (down, routes
	// kept) does not take the name.
	addRouterApp(t, s, "v2", false)
	time.Sleep(200 * time.Millisecond) // let the gateway reload
	if code, body := get(t, addr, "bob.lan", "/p"); code != 200 || !strings.HasPrefix(body, "api-v1 /p") {
		t.Errorf("with a down v2 = %d %q", code, body)
	}

	// Both running: a conflict to fix with another domain, not a guess.
	fakeSession(t, s, "v2", "10.12.12.4", 3200, "api-v2")
	code, body := get(t, addr, "bob.lan", "/")
	if code != 404 || !strings.Contains(body, "routers v1, v2") || !strings.Contains(body, "another domain") {
		t.Errorf("two live routers = %d %q", code, body)
	}
	if code, body := get(t, addr, "nope.lan", "/"); code != 404 || !strings.Contains(body, "no route") {
		t.Errorf("unknown = %d %q", code, body)
	}

	// The host DNS answers registered names with 127.0.0.1.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if ips, err := resolverFor(g.HostDNSAddr()).LookupHost(ctx, "bob.lan"); err != nil || ips[0] != "127.0.0.1" {
		t.Errorf("host dns = %v, %v", ips, err)
	}

	// Unregistering closes :80 again.
	if err := s.SaveRegistration(reg.Without("lan")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !port80.Load() })
}
