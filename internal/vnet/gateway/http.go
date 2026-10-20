package gateway

import (
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"github.com/asc/sax/internal/vnet"
)

const localhostSuffix = ".localhost"

// serveHost handles host-side requests: <host>.<router>.localhost always,
// and names under registered domains (app1.lan, app1.v2.lan).
func (g *Gateway) serveHost(w http.ResponseWriter, req *http.Request) {
	if routerName, host, ok := parseLocalhostHost(req.Host); ok {
		g.serve(w, req, routerName, host)
		return
	}
	routerName, host, msg := g.resolveDomainHost(req.Host)
	if routerName != "" {
		g.serve(w, req, routerName, host)
		return
	}
	if msg == "" {
		msg = fmt.Sprintf("sax vnet gateway: use http://<host>.<router>%s or register a domain "+
			"with sax net register (got %q)", localhostSuffix, req.Host)
	}
	http.Error(w, msg, http.StatusNotFound)
}

// parseLocalhostHost splits "bob.lan.v1.localhost[:port]" into router "v1"
// and host "bob.lan".
func parseLocalhostHost(hostHeader string) (router, host string, ok bool) {
	h := strings.ToLower(hostHeader)
	if name, _, err := net.SplitHostPort(h); err == nil {
		h = name
	}
	rest, found := strings.CutSuffix(h, localhostSuffix)
	if !found {
		return "", "", false
	}
	i := strings.LastIndexByte(rest, '.')
	if i <= 0 || i == len(rest)-1 {
		return "", "", false
	}
	return rest[i+1:], rest[:i], true
}

// serve routes one request for host inside a router: first matching rule
// wins, the path is forwarded unchanged and the Host header is preserved.
func (g *Gateway) serve(w http.ResponseWriter, req *http.Request, routerName, host string) {
	r, ok := g.router(routerName)
	if !ok {
		http.Error(w, fmt.Sprintf("sax vnet: no router %q", routerName), http.StatusNotFound)
		return
	}
	rules, err := r.ParsedRules()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	rule, ok := vnet.MatchHTTP(rules, host, req.URL.Path)
	if !ok {
		http.Error(w, fmt.Sprintf("sax vnet: no route for %s%s in router %s", host, req.URL.Path, routerName),
			http.StatusNotFound)
		return
	}
	backend, err := g.backend(r, rule.Target)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(&url.URL{Scheme: "http", Host: backend})
			pr.Out.Host = pr.In.Host
			pr.SetXForwarded()
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			http.Error(w, fmt.Sprintf("sax vnet: %s in router %s: %v", rule.Target, routerName, err),
				http.StatusBadGateway)
		},
	}
	proxy.ServeHTTP(w, req)
}

// backend resolves a target to the real loopback address of its listener.
func (g *Gateway) backend(r vnet.Router, target vnet.Endpoint) (string, error) {
	ip, ok := r.Resolve(target)
	if !ok {
		return "", fmt.Errorf("sax vnet: %s is not a session in router %s", target.Host, r.Name)
	}
	real, ok := vnet.LookupPort(g.cfg.Store.RouterDir(r.Name), ip, int(target.Port))
	if !ok {
		return "", fmt.Errorf("sax vnet: %s is not listening in router %s", target, r.Name)
	}
	return fmt.Sprintf("127.0.0.1:%d", real), nil
}
