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

// proxyTransport forwards requests as the client sent them: Go's default
// transport would ask for gzip on its own and unpack it, which hides what
// the app really sent and costs time for nothing on loopback.
var proxyTransport = func() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.DisableCompression = true
	return t
}()

// serveHost handles host-side requests by hostname alone: the route's host
// (bob.lan) once its domain is registered, or <host>.localhost (browsers
// send *.localhost to this machine) on the gateway port. Router names are
// never part of a URL: the router is the one whose routes have the host.
func (g *Gateway) serveHost(w http.ResponseWriter, req *http.Request) {
	host := hostOnlyLower(req.Host)
	if h, ok := strings.CutSuffix(host, localhostSuffix); ok {
		host = h
	}
	routerName, msg := g.routerForHost(host, req.URL.Path)
	if routerName == "" {
		http.Error(w, msg, http.StatusNotFound)
		return
	}
	g.serve(w, req, routerName, host)
}

// hostOnlyLower is a Host header without port or trailing dot, lowercased.
func hostOnlyLower(h string) string {
	h = strings.ToLower(h)
	if name, _, err := net.SplitHostPort(h); err == nil {
		h = name
	}
	return strings.TrimSuffix(h, ".")
}

// routerForHost picks the router serving host. When several routers have
// the host (one is down but keeps its routes), the one whose app is
// listening wins; two live ones are a conflict to fix in the projects.
func (g *Gateway) routerForHost(host, path string) (string, string) {
	owners := g.routersWithHTTPHost(host)
	switch len(owners) {
	case 0:
		return "", fmt.Sprintf("sax vnet: no route for %s (see sax net projects / sax net inspect)", host)
	case 1:
		return owners[0], ""
	}
	var live []string
	for _, name := range owners {
		r, ok := g.router(name)
		if !ok {
			continue
		}
		rules, err := r.ParsedRules()
		if err != nil {
			continue
		}
		if rule, ok := vnet.MatchHTTP(rules, host, path); ok {
			if _, err := g.backend(r, rule.Target); err == nil {
				live = append(live, name)
			}
		}
	}
	switch len(live) {
	case 0:
		return owners[0], "" // serve explains what is not listening
	case 1:
		return live[0], ""
	}
	return "", fmt.Sprintf("sax vnet: %s is served by routers %s at once; give one of their projects "+
		"another domain (domain: ...)", host, strings.Join(live, ", "))
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

	banner := wantsBanner(r, req)
	proxy := &httputil.ReverseProxy{
		Transport: proxyTransport,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(&url.URL{Scheme: "http", Host: backend})
			pr.Out.Host = pr.In.Host
			pr.SetXForwarded()
			if banner {
				pr.Out.Header.Del("Accept-Encoding") // uncompressed HTML, so the banner can go in
			}
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			http.Error(w, fmt.Sprintf("sax vnet: %s in router %s: %v", rule.Target, routerName, err),
				http.StatusBadGateway)
		},
	}
	if banner {
		app := appFor(r, rule.Target)
		proxy.ModifyResponse = func(resp *http.Response) error { return g.addBanner(resp, r, app, req.Host) }
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
