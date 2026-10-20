package gateway

import (
	"fmt"
	"io"
	"net"
	"sort"
	"sync"

	"github.com/asc/sax/internal/vnet"
)

// publisher forwards one host port to a target inside a router.
type publisher struct {
	ln     net.Listener
	mu     sync.Mutex
	router string
	target vnet.Endpoint
}

func (p *publisher) close() { _ = p.ln.Close() }

func (p *publisher) current() (string, vnet.Endpoint) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.router, p.target
}

// syncPublishes opens, retargets and closes host port forwards. A host port
// claimed by two routers goes to the first by name. Callers hold g.mu.
func (g *Gateway) syncPublishes(routers []vnet.Router) {
	type want struct {
		router string
		target vnet.Endpoint
	}
	desired := map[uint16]want{}
	sorted := append([]vnet.Router{}, routers...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	for _, r := range sorted {
		pubs, err := r.ParsedPublish()
		if err != nil {
			g.cfg.Logf("gateway: router %s: %v", r.Name, err)
			continue
		}
		for _, p := range pubs {
			if prev, taken := desired[p.HostPort]; taken {
				g.cfg.Logf("gateway: host port %d wanted by %s and %s; keeping %s", p.HostPort, prev.router, r.Name, prev.router)
				continue
			}
			desired[p.HostPort] = want{router: r.Name, target: p.Target}
		}
	}

	for port, p := range g.publishes {
		if _, keep := desired[port]; !keep {
			p.close()
			delete(g.publishes, port)
		}
	}
	for port, w := range desired {
		if p, ok := g.publishes[port]; ok {
			p.mu.Lock()
			p.router, p.target = w.router, w.target
			p.mu.Unlock()
			continue
		}
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			g.cfg.Logf("gateway: publish %d: %v", port, err)
			continue
		}
		p := &publisher{ln: ln, router: w.router, target: w.target}
		g.publishes[port] = p
		go g.acceptPublish(p)
	}
}

func (g *Gateway) acceptPublish(p *publisher) {
	for {
		conn, err := p.ln.Accept()
		if err != nil {
			return // listener closed
		}
		go g.forward(conn, p)
	}
}

func (g *Gateway) forward(client net.Conn, p *publisher) {
	defer client.Close()
	routerName, target := p.current()
	r, ok := g.router(routerName)
	if !ok {
		return
	}
	addr, err := g.backend(r, target)
	if err != nil {
		g.cfg.Logf("gateway: %v", err)
		return
	}
	upstream, err := net.Dial("tcp", addr)
	if err != nil {
		g.cfg.Logf("gateway: dial %s: %v", target, err)
		return
	}
	defer upstream.Close()
	pipe(client, upstream)
}

// pipe copies both directions, half-closing each side when its source ends.
func pipe(a, b net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	copyHalf := func(dst, src net.Conn) {
		defer wg.Done()
		_, _ = io.Copy(dst, src)
		if tc, ok := dst.(*net.TCPConn); ok {
			_ = tc.CloseWrite()
		}
	}
	go copyHalf(a, b)
	go copyHalf(b, a)
	wg.Wait()
}
