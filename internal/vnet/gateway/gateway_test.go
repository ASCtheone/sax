package gateway

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/asc/sax/internal/vnet"
)

// fakeSession is an HTTP server standing in for a session app, registered in
// the router's listener registry exactly like the shim would.
func fakeSession(t *testing.T, s vnet.Store, router, ip string, vport int, name string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s %s host=%s xff=%s", name, r.URL.RequestURI(), r.Host, r.Header.Get("X-Forwarded-For"))
	}))
	t.Cleanup(srv.Close)
	real := srv.Listener.Addr().(*net.TCPAddr).Port
	if err := vnet.RegisterPort(s.RouterDir(router), ip, vport, real, os.Getpid()); err != nil {
		t.Fatal(err)
	}
}

func setup(t *testing.T) (vnet.Store, *Gateway) {
	t.Helper()
	s := vnet.NewStore(t.TempDir())
	r, _ := vnet.NewRouter("v1", "10.12.12.0/24", "lan")
	if err := s.Create(r); err != nil {
		t.Fatal(err)
	}
	_, err := s.Update("v1", func(r vnet.Router) (vnet.Router, error) {
		r, _, _ = r.WithSession("api", "10.12.12.4")
		r, _, _ = r.WithSession("web", "10.12.12.5")
		for _, rule := range []string{"bob.lan/api/* -> api:3200", "bob.lan/* -> web:4200", "dead.lan/* -> api:9999"} {
			var err error
			if r, err = r.WithRule(rule); err != nil {
				return r, err
			}
		}
		return r, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	fakeSession(t, s, "v1", "10.12.12.4", 3200, "api")
	fakeSession(t, s, "v1", "10.12.12.5", 4200, "web")

	g := New(Config{Store: s, HostAddr: "127.0.0.1:0"})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- g.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("gateway did not stop")
		}
	})
	waitFor(t, func() bool { return g.HostAddr() != "" })
	return s, g
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func get(t *testing.T, addr, host, path string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest("GET", "http://"+addr+path, nil)
	req.Host = host
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s%s via %s: %v", host, path, addr, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

// inRouter returns the address a session would reach for host:80, by
// resolving the host's VIP through the registry like the shim does.
func inRouter(t *testing.T, s vnet.Store, host string) string {
	t.Helper()
	r, _ := s.Get("v1")
	vip := r.VIPs[host]
	var real int
	waitFor(t, func() bool {
		var ok bool
		real, ok = vnet.LookupPort(s.RouterDir("v1"), vip, 80)
		return ok
	})
	return fmt.Sprintf("127.0.0.1:%d", real)
}

func TestGateway_PathRoutingInsideRouter(t *testing.T) {
	s, _ := setup(t)
	addr := inRouter(t, s, "bob.lan")

	cases := map[string]string{
		"/api/users?id=1": "api /api/users?id=1", // path and query forwarded unchanged
		"/api/":           "api /api/",
		"/":               "web /",
		"/api":            "web /api", // Cloudflare semantics: bare /api falls through
		"/dashboard/x":    "web /dashboard/x",
	}
	for path, want := range cases {
		code, body := get(t, addr, "bob.lan", path)
		if code != 200 || !strings.HasPrefix(body, want) {
			t.Errorf("GET bob.lan%s = %d %q, want %q", path, code, body, want)
		}
	}
	_, body := get(t, addr, "bob.lan", "/api/x")
	if !strings.Contains(body, "host=bob.lan") || !strings.Contains(body, "xff=127.0.0.1") {
		t.Errorf("Host header must be preserved and X-Forwarded-For set: %q", body)
	}
}

func TestGateway_Errors(t *testing.T) {
	s, _ := setup(t)
	addr := inRouter(t, s, "dead.lan")
	code, body := get(t, addr, "dead.lan", "/")
	if code != http.StatusBadGateway || !strings.Contains(body, "api:9999") {
		t.Errorf("dead target = %d %q", code, body)
	}
	code, body = get(t, addr, "nobody.lan", "/")
	if code != http.StatusNotFound || !strings.Contains(body, "no route") {
		t.Errorf("unknown host = %d %q", code, body)
	}
}

func TestGateway_HostAccessViaLocalhost(t *testing.T) {
	_, g := setup(t)
	code, body := get(t, g.HostAddr(), "bob.lan.v1.localhost:7480", "/api/users")
	if code != 200 || !strings.HasPrefix(body, "api /api/users") {
		t.Errorf("host access = %d %q", code, body)
	}
	code, _ = get(t, g.HostAddr(), "bob.lan.ghost.localhost", "/")
	if code != http.StatusNotFound {
		t.Errorf("unknown router = %d", code)
	}
	code, body = get(t, g.HostAddr(), "example.com", "/")
	if code != http.StatusNotFound || !strings.Contains(body, ".localhost") {
		t.Errorf("non-localhost host = %d %q", code, body)
	}
}

func TestGateway_PicksUpChanges(t *testing.T) {
	s, _ := setup(t)
	if _, err := s.Update("v1", func(r vnet.Router) (vnet.Router, error) {
		return r.WithRule("new.lan/* -> web:4200")
	}); err != nil {
		t.Fatal(err)
	}
	addr := inRouter(t, s, "new.lan") // waits for the reload
	if code, body := get(t, addr, "new.lan", "/hi"); code != 200 || !strings.HasPrefix(body, "web /hi") {
		t.Errorf("new rule = %d %q", code, body)
	}

	// Removing the router closes its listener and unregisters its VIPs.
	r, _ := s.Get("v1")
	dir, vip := s.RouterDir("v1"), r.VIPs["bob.lan"]
	if err := s.Remove("v1"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			conn.Close()
		}
		return err != nil
	})
	if _, ok := vnet.LookupPort(dir, vip, 80); ok {
		t.Error("VIP still registered after the router was removed")
	}
}

func TestGateway_Publish(t *testing.T) {
	s, _ := setup(t)
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	hostPort := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	if _, err := s.Update("v1", func(r vnet.Router) (vnet.Router, error) {
		return r.WithPublish(fmt.Sprintf("%d -> api:3200", hostPort))
	}); err != nil {
		t.Fatal(err)
	}
	addr := fmt.Sprintf("127.0.0.1:%d", hostPort)
	var conn net.Conn
	waitFor(t, func() bool {
		var err error
		conn, err = net.DialTimeout("tcp", addr, 200*time.Millisecond)
		return err == nil
	})
	defer conn.Close()
	fmt.Fprintf(conn, "GET /published HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n")
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.HasPrefix(string(body), "api /published") {
		t.Errorf("published port body = %q", body)
	}
}

func TestParseLocalhostHost(t *testing.T) {
	cases := map[string][2]string{
		"bob.lan.v1.localhost":      {"v1", "bob.lan"},
		"bob.lan.v1.localhost:7480": {"v1", "bob.lan"},
		"API.V2.LOCALHOST":          {"v2", "api"},
	}
	for in, want := range cases {
		router, host, ok := parseLocalhostHost(in)
		if !ok || router != want[0] || host != want[1] {
			t.Errorf("parseLocalhostHost(%q) = %q %q %v", in, router, host, ok)
		}
	}
	for _, bad := range []string{"v1.localhost", "localhost", "bob.lan.v1.example.com", ""} {
		if _, _, ok := parseLocalhostHost(bad); ok {
			t.Errorf("parseLocalhostHost(%q) should fail", bad)
		}
	}
}

// HMR (Vite, Angular, Next) runs over WebSockets: the upgrade must pass
// through the gateway and the connection must stay open both ways.
func TestGateway_WebSocketUpgrade(t *testing.T) {
	s, _ := setup(t)
	echo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") != "websocket" {
			http.Error(w, "want upgrade", http.StatusBadRequest)
			return
		}
		conn, buf, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		fmt.Fprint(buf, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
		_ = buf.Flush()
		_, _ = io.Copy(conn, buf) // echo
	}))
	t.Cleanup(echo.Close)
	real := echo.Listener.Addr().(*net.TCPAddr).Port
	if err := vnet.RegisterPort(s.RouterDir("v1"), "10.12.12.5", 4300, real, os.Getpid()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Update("v1", func(r vnet.Router) (vnet.Router, error) {
		return r.WithRule("hmr.lan/* -> web:4300")
	}); err != nil {
		t.Fatal(err)
	}

	conn, err := net.Dial("tcp", inRouter(t, s, "hmr.lan"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fmt.Fprint(conn, "GET /ws HTTP/1.1\r\nHost: hmr.lan\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil || resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade = %v, %v", resp, err)
	}
	fmt.Fprint(conn, "ping")
	got := make([]byte, 4)
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadFull(br, got); err != nil || string(got) != "ping" {
		t.Errorf("echo = %q, %v", got, err)
	}
}

func TestListenHost_WildcardIsLoopbackOnly(t *testing.T) {
	ln, err := listenHost("0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if _, ok := ln.(loopbackOnly); !ok {
		t.Fatal("wildcard listener must be wrapped")
	}
	port := ln.Addr().(*net.TCPAddr).Port
	go func() {
		c, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			c.Close()
		}
	}()
	conn, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()

	lo, err := listenHost("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer lo.Close()
	if _, wrapped := lo.(loopbackOnly); wrapped {
		t.Error("loopback listener needs no wrapper")
	}
}
