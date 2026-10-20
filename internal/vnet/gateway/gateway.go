// Package gateway serves vnet traffic that needs a process in the path: HTTP
// path routes (first match wins), host access through *.localhost, host
// port publishes, and a per-router DNS stub for supervised Linux sessions. One gateway serves every router; it runs as the sax session
// "vnet-gateway" and reloads router state from the store every second, so
// `sax net map` and `sax net up` take effect without a restart.
//
// Inside a router, sessions reach the gateway through the shim: for every
// HTTP host the gateway registers <host VIP>:80 in that router's listener
// registry, pointing at a loopback listener dedicated to the router.
package gateway

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/asc/sax/internal/vnet"
)

const (
	// DefaultHostAddr serves http://<host>.<router>.localhost:7480 from the host.
	DefaultHostAddr = "127.0.0.1:7480"
	// SessionName is the sax session that runs the gateway.
	SessionName = "vnet-gateway"

	defaultReload     = time.Second
	httpPort          = 80
	readHeaderTimeout = 10 * time.Second
)

// Config configures a gateway.
type Config struct {
	Store    vnet.Store
	HostAddr string        // host-facing HTTP listener; DefaultHostAddr if empty
	Reload   time.Duration // store polling interval; 1s if zero
	Logf     func(format string, args ...any)
	// DNSUpstream and DNSSearch default to the host's /etc/resolv.conf.
	DNSUpstream []string
	DNSSearch   []string
	// HostDNSAddr serves registered domains (sax net register) to the OS
	// resolver; "" disables it.
	HostDNSAddr string
	// Port80Addr is bound while a domain is registered, so names work without
	// a port. Defaults to 0.0.0.0:80 (loopback peers only) on macOS, where
	// that needs no root, and 127.0.0.1:80 elsewhere.
	Port80Addr string
	// OnPort80 is told when the :80 listener opens or closes.
	OnPort80 func(on bool)
}

// Gateway is the running HTTP router and port forwarder.
type Gateway struct {
	cfg      Config
	routers  atomic.Pointer[map[string]vnet.Router]
	hostAddr atomic.Value // string, set once the host listener is up

	registration atomic.Pointer[vnet.Registration]

	mu           sync.Mutex
	hostDNS      *hostDNS
	port80       *http.Server
	port80Ln     net.Listener
	port80Warned bool
	listeners    map[string]*routerListener // router name -> in-router HTTP listener
	dns          map[string]*dnsServer      // router name -> DNS stub
	publishes    map[uint16]*publisher      // host port -> forwarder
}

// routerListener serves one router's HTTP hosts for its sessions.
type routerListener struct {
	ln   net.Listener
	srv  *http.Server
	vips map[string]bool // VIPs currently registered for :80
}

// New builds a gateway; call Run to start it.
func New(cfg Config) *Gateway {
	if cfg.HostAddr == "" {
		cfg.HostAddr = DefaultHostAddr
	}
	if cfg.Reload <= 0 {
		cfg.Reload = defaultReload
	}
	if cfg.Logf == nil {
		cfg.Logf = log.Printf
	}
	if cfg.DNSUpstream == nil && cfg.DNSSearch == nil {
		cfg.DNSUpstream, cfg.DNSSearch = hostResolvers(resolvConf)
	}
	if cfg.Port80Addr == "" {
		cfg.Port80Addr = net.JoinHostPort("127.0.0.1", httpPort80)
		if runtime.GOOS == "darwin" {
			cfg.Port80Addr = net.JoinHostPort(wildcardHost, httpPort80)
		}
	}
	g := &Gateway{
		cfg:       cfg,
		listeners: map[string]*routerListener{},
		dns:       map[string]*dnsServer{},
		publishes: map[uint16]*publisher{},
	}
	empty := map[string]vnet.Router{}
	g.routers.Store(&empty)
	g.registration.Store(&vnet.Registration{})
	return g
}

// HostAddr returns the bound host-facing address, or "" before Run binds it.
func (g *Gateway) HostAddr() string {
	v, _ := g.hostAddr.Load().(string)
	return v
}

// Run serves until ctx is cancelled, then unregisters everything it added.
func (g *Gateway) Run(ctx context.Context) error {
	ln, err := listenHost(g.cfg.HostAddr)
	if err != nil {
		return fmt.Errorf("gateway: listen %s: %w", g.cfg.HostAddr, err)
	}
	hostSrv := &http.Server{Handler: http.HandlerFunc(g.serveHost), ReadHeaderTimeout: readHeaderTimeout}
	go func() {
		if err := hostSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			g.cfg.Logf("gateway: host listener: %v", err)
		}
	}()
	g.hostAddr.Store(ln.Addr().String())
	if g.cfg.HostDNSAddr != "" {
		h, err := newHostDNS(g.cfg.HostDNSAddr, g.cfg.Logf)
		if err != nil {
			g.cfg.Logf("gateway: host dns %s: %v (registered domains will not resolve)", g.cfg.HostDNSAddr, err)
		} else {
			g.mu.Lock()
			g.hostDNS = h
			g.mu.Unlock()
		}
	}

	ticker := time.NewTicker(g.cfg.Reload)
	defer ticker.Stop()
	for {
		if err := g.Sync(); err != nil {
			g.cfg.Logf("gateway: sync: %v", err)
		}
		select {
		case <-ctx.Done():
			_ = hostSrv.Close()
			g.shutdown()
			return nil
		case <-ticker.C:
		}
	}
}

// Sync reloads routers from the store and reconciles listeners, VIP
// registrations and publishes with them.
func (g *Gateway) Sync() error {
	list, err := g.cfg.Store.List()
	if err != nil {
		return err
	}
	routers := make(map[string]vnet.Router, len(list))
	for _, r := range list {
		routers[r.Name] = r
	}
	g.routers.Store(&routers)

	g.mu.Lock()
	defer g.mu.Unlock()
	g.syncHostNames()
	for _, r := range list {
		tables, _ := r.Tables()
		g.syncDNS(r, tables.Names)
		if len(tables.HTTP) == 0 {
			g.closeRouter(r.Name)
			continue
		}
		if err := g.syncRouter(r, tables.HTTP); err != nil {
			g.cfg.Logf("gateway: router %s: %v", r.Name, err)
		}
	}
	for name := range g.listeners {
		if _, ok := routers[name]; !ok {
			g.closeRouter(name)
		}
	}
	for name := range g.dns {
		if _, ok := routers[name]; !ok {
			g.closeDNS(name)
		}
	}
	g.syncPublishes(list)
	return nil
}

func (g *Gateway) syncRouter(r vnet.Router, hosts []vnet.HTTPHost) error {
	rl, ok := g.listeners[r.Name]
	if !ok {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return fmt.Errorf("listen: %w", err)
		}
		name := r.Name
		srv := &http.Server{
			Handler: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				g.serve(w, req, name, req.Host)
			}),
			ReadHeaderTimeout: readHeaderTimeout,
		}
		go func() { _ = srv.Serve(ln) }()
		rl = &routerListener{ln: ln, srv: srv, vips: map[string]bool{}}
		g.listeners[r.Name] = rl
	}

	dir := g.cfg.Store.RouterDir(r.Name)
	real := rl.ln.Addr().(*net.TCPAddr).Port
	want := map[string]bool{}
	for _, h := range hosts {
		want[h.VIP] = true
		// Re-registering every sync is cheap and heals a deleted entry.
		if err := vnet.RegisterPort(dir, h.VIP, httpPort, real, os.Getpid()); err != nil {
			return fmt.Errorf("register %s: %w", h.Host, err)
		}
	}
	for vip := range rl.vips {
		if !want[vip] {
			vnet.UnregisterPort(dir, vip, httpPort)
		}
	}
	rl.vips = want
	return nil
}

// closeRouter stops a router's listener and drops its registrations. Callers
// hold g.mu.
func (g *Gateway) closeRouter(name string) {
	rl, ok := g.listeners[name]
	if !ok {
		return
	}
	dir := g.cfg.Store.RouterDir(name)
	for vip := range rl.vips {
		vnet.UnregisterPort(dir, vip, httpPort)
	}
	_ = rl.srv.Close()
	delete(g.listeners, name)
}

func (g *Gateway) shutdown() {
	g.mu.Lock()
	defer g.mu.Unlock()
	for name := range g.listeners {
		g.closeRouter(name)
	}
	for name := range g.dns {
		g.closeDNS(name)
	}
	if g.hostDNS != nil {
		g.hostDNS.close()
		g.hostDNS = nil
	}
	g.syncPort80(false)
	for port, p := range g.publishes {
		p.close()
		delete(g.publishes, port)
	}
}

func (g *Gateway) router(name string) (vnet.Router, bool) {
	r, ok := (*g.routers.Load())[name]
	return r, ok
}
