package gateway

import (
	"bufio"
	"encoding/binary"
	"errors"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/asc/sax/internal/vnet"
)

// A per-router DNS stub for supervised sessions on Linux: programs that do
// their own DNS (Go) send queries to the nameserver in /etc/resolv.conf, and
// the supervisor redirects them here. Router names get an A record; anything
// else is relayed to the real resolvers unchanged.

const (
	dnsHeaderLen   = 12
	dnsTypeA       = 1
	dnsClassIN     = 1
	dnsTTL         = 5
	dnsMaxPacket   = 65535
	dnsUpstreamTTL = 2 * time.Second
	resolvConf     = "/etc/resolv.conf"
	dnsPort        = "53"
)

type dnsServer struct {
	conn     *net.UDPConn
	names    atomic.Pointer[map[string]string] // lowercase name -> IPv4
	upstream []string
	search   []string
	logf     func(string, ...any)
}

func newDNSServer(upstream, search []string, logf func(string, ...any)) (*dnsServer, error) {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		return nil, err
	}
	d := &dnsServer{conn: conn, upstream: upstream, search: search, logf: logf}
	empty := map[string]string{}
	d.names.Store(&empty)
	go d.serve()
	return d, nil
}

func (d *dnsServer) port() int { return d.conn.LocalAddr().(*net.UDPAddr).Port }

func (d *dnsServer) close() { _ = d.conn.Close() }

func (d *dnsServer) setNames(entries []vnet.NameEntry) {
	names := make(map[string]string, len(entries))
	for _, e := range entries {
		names[strings.ToLower(e.Name)] = e.IP
	}
	d.names.Store(&names)
}

// lookup resolves a router name, also when the resolver appended one of the
// host's search domains ("api" queried as "api.corp.example").
func (d *dnsServer) lookup(name string) (string, bool) {
	name = strings.TrimSuffix(strings.ToLower(name), ".")
	names := *d.names.Load()
	if ip, ok := names[name]; ok {
		return ip, true
	}
	for _, s := range d.search {
		if short, ok := strings.CutSuffix(name, "."+s); ok {
			if ip, ok := names[short]; ok {
				return ip, true
			}
		}
	}
	return "", false
}

func (d *dnsServer) serve() {
	buf := make([]byte, dnsMaxPacket)
	for {
		n, addr, err := d.conn.ReadFromUDP(buf)
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				d.logf("gateway: dns: %v", err)
			}
			return
		}
		pkt := append([]byte{}, buf[:n]...)
		go func() {
			if resp := d.respond(pkt); resp != nil {
				_, _ = d.conn.WriteToUDP(resp, addr)
			}
		}()
	}
}

func (d *dnsServer) respond(query []byte) []byte {
	name, qtype, qend, ok := parseQuestion(query)
	if !ok {
		return nil
	}
	if ip, known := d.lookup(name); known {
		var a net.IP
		if qtype == dnsTypeA {
			a = net.ParseIP(ip).To4()
		}
		return dnsAnswer(query[:qend], a, 0)
	}
	if resp := d.forward(query); resp != nil {
		return resp
	}
	const servfail = 2
	return dnsAnswer(query[:qend], nil, servfail)
}

// forward relays a query to the first upstream resolver that answers.
func (d *dnsServer) forward(query []byte) []byte {
	buf := make([]byte, dnsMaxPacket)
	for _, up := range d.upstream {
		conn, err := net.DialTimeout("udp", up, dnsUpstreamTTL)
		if err != nil {
			continue
		}
		_ = conn.SetDeadline(time.Now().Add(dnsUpstreamTTL))
		if _, err := conn.Write(query); err == nil {
			if n, err := conn.Read(buf); err == nil {
				conn.Close()
				return append([]byte{}, buf[:n]...)
			}
		}
		conn.Close()
	}
	return nil
}

// parseQuestion reads the single question of a standard query. qend is the
// offset just past it, so header+question can be echoed in the response.
func parseQuestion(pkt []byte) (name string, qtype uint16, qend int, ok bool) {
	if len(pkt) < dnsHeaderLen || binary.BigEndian.Uint16(pkt[4:6]) != 1 {
		return "", 0, 0, false
	}
	var labels []string
	i := dnsHeaderLen
	for {
		if i >= len(pkt) {
			return "", 0, 0, false
		}
		l := int(pkt[i])
		i++
		if l == 0 {
			break
		}
		if l&0xC0 != 0 || i+l > len(pkt) { // no compression in a question
			return "", 0, 0, false
		}
		labels = append(labels, string(pkt[i:i+l]))
		i += l
	}
	if i+4 > len(pkt) {
		return "", 0, 0, false
	}
	qtype = binary.BigEndian.Uint16(pkt[i : i+2])
	return strings.Join(labels, "."), qtype, i + 4, true
}

// dnsAnswer builds a response from the query's header and question: one A
// record when ip is set, otherwise an empty answer with the given rcode.
func dnsAnswer(headerAndQuestion []byte, ip net.IP, rcode byte) []byte {
	resp := append([]byte{}, headerAndQuestion...)
	const qr, aa, ra, opcodeAndRD = 0x80, 0x04, 0x80, 0x79
	resp[2] = qr | aa | (headerAndQuestion[2] & opcodeAndRD)
	resp[3] = ra | rcode
	var answers uint16
	if ip != nil {
		answers = 1
	}
	binary.BigEndian.PutUint16(resp[6:8], answers)
	binary.BigEndian.PutUint16(resp[8:10], 0)  // authority
	binary.BigEndian.PutUint16(resp[10:12], 0) // additional (drops EDNS)
	if ip == nil {
		return resp
	}
	rr := []byte{0xC0, dnsHeaderLen} // name: pointer to the question
	rr = binary.BigEndian.AppendUint16(rr, dnsTypeA)
	rr = binary.BigEndian.AppendUint16(rr, dnsClassIN)
	rr = binary.BigEndian.AppendUint32(rr, dnsTTL)
	rr = binary.BigEndian.AppendUint16(rr, uint16(len(ip)))
	return append(append(resp, rr...), ip...)
}

// hostResolvers reads nameservers and search domains from resolv.conf.
func hostResolvers(path string) (upstream, search []string) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		switch fields[0] {
		case "nameserver":
			upstream = append(upstream, net.JoinHostPort(fields[1], dnsPort))
		case "search", "domain":
			for _, s := range fields[1:] {
				search = append(search, strings.ToLower(strings.TrimSuffix(s, ".")))
			}
		}
	}
	return upstream, search
}

// syncDNS starts or refreshes a router's DNS stub and publishes its port in
// the router dir for the supervisor. Callers hold g.mu.
func (g *Gateway) syncDNS(r vnet.Router, names []vnet.NameEntry) {
	d, ok := g.dns[r.Name]
	if !ok {
		var err error
		if d, err = newDNSServer(g.cfg.DNSUpstream, g.cfg.DNSSearch, g.cfg.Logf); err != nil {
			g.cfg.Logf("gateway: router %s: dns: %v", r.Name, err)
			return
		}
		g.dns[r.Name] = d
	}
	d.setNames(names)
	dir := g.cfg.Store.RouterDir(r.Name)
	if ok && vnet.DNSRegistered(dir) {
		return
	}
	if err := vnet.RegisterDNS(dir, d.port(), os.Getpid()); err != nil {
		g.cfg.Logf("gateway: router %s: dns: %v", r.Name, err)
	}
}

// closeDNS stops a router's DNS stub. Callers hold g.mu.
func (g *Gateway) closeDNS(name string) {
	d, ok := g.dns[name]
	if !ok {
		return
	}
	vnet.UnregisterDNS(g.cfg.Store.RouterDir(name))
	d.close()
	delete(g.dns, name)
}
