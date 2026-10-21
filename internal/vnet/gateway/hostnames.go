package gateway

import (
	"errors"
	"net"
	"net/http"
	"sort"
	"sync/atomic"

	"github.com/asc/sax/internal/vnet"
)

// Registered domains (sax net register): the OS resolver sends lookups for
// e.g. ".lan" to hostDNS, which answers 127.0.0.1, and the browser lands on
// the gateway with Host "app1.lan". Unlike the per-router stub, hostDNS
// never forwards: on Linux systemd-resolved could hand the query straight
// back to it.

const (
	dnsTypeAAAA  = 28
	dnsRefused   = 5
	httpPort80   = "80"
	wildcardHost = "0.0.0.0"
)

var loopbackA = net.IPv4(127, 0, 0, 1).To4()

type hostDNS struct {
	conn    *net.UDPConn
	domains atomic.Pointer[vnet.Registration]
	logf    func(string, ...any)
}

func newHostDNS(addr string, logf func(string, ...any)) (*hostDNS, error) {
	ua, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, err
	}
	conn, err := net.ListenUDP("udp", ua)
	if err != nil {
		return nil, err
	}
	h := &hostDNS{conn: conn, logf: logf}
	h.domains.Store(&vnet.Registration{})
	go h.serve()
	return h, nil
}

func (h *hostDNS) close() { _ = h.conn.Close() }

func (h *hostDNS) serve() {
	buf := make([]byte, dnsMaxPacket)
	for {
		n, addr, err := h.conn.ReadFromUDP(buf)
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				h.logf("gateway: host dns: %v", err)
			}
			return
		}
		if resp := h.respond(buf[:n]); resp != nil {
			_, _ = h.conn.WriteToUDP(resp, addr)
		}
	}
}

func (h *hostDNS) respond(query []byte) []byte {
	name, qtype, qend, ok := parseQuestion(query)
	if !ok {
		return nil
	}
	if _, covered := h.domains.Load().Covers(name); !covered {
		return dnsAnswer(query[:qend], nil, dnsRefused)
	}
	if qtype == dnsTypeA {
		return dnsAnswer(query[:qend], loopbackA, 0)
	}
	return dnsAnswer(query[:qend], nil, 0) // AAAA etc.: no data, so clients use IPv4
}

// syncHostNames applies the registration: domains for hostDNS and whether
// the gateway should also serve plain port 80. Callers hold g.mu.
func (g *Gateway) syncHostNames() {
	reg, err := g.cfg.Store.Registration()
	if err != nil {
		g.cfg.Logf("gateway: %v", err)
		return
	}
	g.registration.Store(&reg)
	if g.hostDNS != nil {
		g.hostDNS.domains.Store(&reg)
	}
	g.syncPort80(len(reg.Domains) > 0)
}

// syncPort80 opens or closes the extra :80 listener so registered names work
// without a port. Callers hold g.mu.
func (g *Gateway) syncPort80(want bool) {
	if _, port, _ := net.SplitHostPort(g.HostAddr()); port == httpPort80 {
		want = false // the main listener already is :80
	}
	switch {
	case want && g.port80 == nil:
		ln, err := listenHost(g.cfg.Port80Addr)
		if err != nil {
			if !g.port80Warned {
				g.port80Warned = true
				g.cfg.Logf("gateway: port 80 unavailable (%v); registered names need :%s", err, portOf(g.HostAddr()))
			}
			return
		}
		g.port80Warned = false
		srv := &http.Server{Handler: http.HandlerFunc(g.serveHost), ReadHeaderTimeout: readHeaderTimeout}
		go func() { _ = srv.Serve(ln) }()
		g.port80, g.port80Ln = srv, ln
		g.notifyPort80(true)
	case !want && g.port80 != nil:
		_ = g.port80.Close()
		g.port80, g.port80Ln = nil, nil
		g.notifyPort80(false)
	}
}

func (g *Gateway) notifyPort80(on bool) {
	if g.cfg.OnPort80 != nil {
		g.cfg.OnPort80(on)
	}
}

func portOf(addr string) string {
	_, port, _ := net.SplitHostPort(addr)
	return port
}

func (g *Gateway) routersWithHTTPHost(host string) []string {
	var out []string
	for name, r := range *g.routers.Load() {
		tables, _ := r.Tables()
		for _, h := range tables.HTTP {
			if h.Host == host {
				out = append(out, name)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

// Port80Addr returns the bound :80 listener address, or "" when closed.
func (g *Gateway) Port80Addr() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.port80 == nil || g.port80Ln == nil {
		return ""
	}
	return g.port80Ln.Addr().String()
}

// HostDNSAddr returns the bound host DNS address, or "" when not running.
func (g *Gateway) HostDNSAddr() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.hostDNS == nil {
		return ""
	}
	return g.hostDNS.conn.LocalAddr().String()
}
