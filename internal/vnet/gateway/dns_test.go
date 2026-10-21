package gateway

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/asc/sax/internal/vnet"
)

// fakeUpstream stands in for the host's real resolver: it knows names under
// example.invalid (192.0.2.7) and answers NXDOMAIN for everything else.
func fakeUpstream(t *testing.T) string {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	go func() {
		buf := make([]byte, 512)
		for {
			n, addr, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			name, _, qend, ok := parseQuestion(buf[:n])
			if !ok {
				continue
			}
			if strings.HasSuffix(strings.TrimSuffix(name, "."), "example.invalid") {
				_, _ = conn.WriteToUDP(dnsAnswer(buf[:qend], net.IPv4(192, 0, 2, 7).To4(), 0), addr)
				continue
			}
			const nxdomain = 3
			_, _ = conn.WriteToUDP(dnsAnswer(buf[:qend], nil, nxdomain), addr)
		}
	}()
	return conn.LocalAddr().String()
}

// resolverFor sends every query to the given DNS address, like the
// supervisor's redirect does for a session.
func resolverFor(addr string) *net.Resolver {
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "udp", addr)
		},
	}
}

func TestDNSStub(t *testing.T) {
	up := fakeUpstream(t)
	d, err := newDNSServer([]string{up}, []string{"corp.example"}, t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	defer d.close()
	d.setNames([]vnet.NameEntry{{Name: "api", IP: "10.12.12.4"}, {Name: "bob.lan", IP: "10.12.12.254"}})
	res := resolverFor(fmt.Sprintf("127.0.0.1:%d", d.port()))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cases := map[string]string{
		"api":                  "10.12.12.4",
		"API.":                 "10.12.12.4",   // case and trailing dot
		"bob.lan":              "10.12.12.254", // rule host VIP
		"api.corp.example":     "10.12.12.4",   // host search domain appended
		"www.example.invalid.": "192.0.2.7",    // relayed upstream
	}
	for name, want := range cases {
		addrs, err := res.LookupHost(ctx, name)
		if err != nil || !reflect.DeepEqual(addrs, []string{want}) {
			t.Errorf("LookupHost(%q) = %v, %v; want [%s]", name, addrs, err, want)
		}
	}
}

func TestDNSStub_NoUpstream(t *testing.T) {
	d, err := newDNSServer(nil, nil, t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	defer d.close()
	res := resolverFor(fmt.Sprintf("127.0.0.1:%d", d.port()))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := res.LookupHost(ctx, "nowhere.example"); err == nil {
		t.Error("unknown name without upstream should fail")
	}
}

func TestParseQuestion_Malformed(t *testing.T) {
	for _, pkt := range [][]byte{nil, make([]byte, 11), append(make([]byte, 12), 0xC0, 0x0C)} {
		if _, _, _, ok := parseQuestion(pkt); ok {
			t.Errorf("parseQuestion(%x) should fail", pkt)
		}
	}
}

func TestHostResolvers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resolv.conf")
	_ = os.WriteFile(path, []byte("# c\nnameserver 127.0.0.53\nnameserver ::1\nsearch Corp.Example. lan\noptions edns0\n"), 0o644)
	up, search := hostResolvers(path)
	if !reflect.DeepEqual(up, []string{"127.0.0.53:53", "[::1]:53"}) {
		t.Errorf("upstream = %v", up)
	}
	if !reflect.DeepEqual(search, []string{"corp.example", "lan"}) {
		t.Errorf("search = %v", search)
	}
	if up, search := hostResolvers(filepath.Join(t.TempDir(), "missing")); up != nil || search != nil {
		t.Error("missing resolv.conf should yield nothing")
	}
}

// The gateway runs a DNS stub per router and publishes its port.
func TestGateway_DNSPerRouter(t *testing.T) {
	s, _ := setup(t)
	dir := s.RouterDir("v1")
	waitFor(t, func() bool { return vnet.DNSRegistered(dir) })
	data, _ := os.ReadFile(filepath.Join(dir, "dns"))
	var port, pid int
	if _, err := fmt.Sscanf(string(data), "%d %d", &port, &pid); err != nil || pid != os.Getpid() {
		t.Fatalf("dns file = %q", data)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res := resolverFor(fmt.Sprintf("127.0.0.1:%d", port))
	addrs, err := res.LookupHost(ctx, "web.")
	if err != nil || strings.Join(addrs, ",") != "10.12.12.5" {
		t.Errorf("web = %v, %v", addrs, err)
	}
	if addrs, err := res.LookupHost(ctx, "web.lan."); err == nil {
		t.Errorf("apps get no <app>.<domain> name (the domain is for routes): %v", addrs)
	}

	if err := s.Remove("v1"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		c, err := net.Dial("udp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			return true
		}
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(200 * time.Millisecond))
		_, _ = c.Write([]byte{0, 1, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 1, 0, 1})
		_, err = c.Read(make([]byte, 64))
		return err != nil // stub gone: no answer
	})
}
