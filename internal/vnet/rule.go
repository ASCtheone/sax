package vnet

import (
	"fmt"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
)

// Kind distinguishes plain TCP name mappings from HTTP path routes.
type Kind int

const (
	// KindTCP maps host:port straight to a target (no proxy in the path).
	KindTCP Kind = iota
	// KindHTTP routes requests for host/path through the router's HTTP proxy.
	KindHTTP
)

const (
	httpPort   = 80
	ruleArrow  = "->"
	wildcard   = '*'
	queryStart = '?'
)

var (
	labelRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
)

// Endpoint is a rule target: a session name (resolved through the router) or
// an IPv4 literal inside the router subnet, plus a port.
type Endpoint struct {
	Host string
	Port uint16
}

func (e Endpoint) String() string { return fmt.Sprintf("%s:%d", e.Host, e.Port) }

// Rule is one parsed routing entry. Rules are evaluated in the order they are
// written; for HTTP the first matching rule wins (Cloudflare style).
type Rule struct {
	Kind   Kind
	Host   string // lowercase hostname the rule answers for
	Port   uint16 // port on the host's service VIP (80 for HTTP)
	Path   string // HTTP only: path pattern, '*' matches any run of characters
	Target Endpoint
}

// Pattern returns the left-hand side of the rule in canonical form.
func (r Rule) Pattern() string {
	if r.Kind == KindHTTP {
		return r.Host + r.Path
	}
	if r.Port == httpPort {
		return r.Host
	}
	return fmt.Sprintf("%s:%d", r.Host, r.Port)
}

// String returns the canonical "pattern -> target" form.
func (r Rule) String() string {
	return fmt.Sprintf("%s %s %s", r.Pattern(), ruleArrow, r.Target)
}

// ParseRule parses "pattern -> target".
//
//	bob.lan/api/* -> api:3200   HTTP route (pattern contains a path)
//	db.lan:5432   -> db:5432    TCP mapping
//	bob.lan       -> api:3200   TCP mapping on port 80
func ParseRule(s string) (Rule, error) {
	pattern, target, ok := strings.Cut(s, ruleArrow)
	if !ok {
		return Rule{}, fmt.Errorf("rule %q: want \"pattern %s target\"", s, ruleArrow)
	}
	return NewRule(pattern, target)
}

// NewRule builds a rule from its two halves.
func NewRule(pattern, target string) (Rule, error) {
	pattern = strings.TrimSpace(pattern)
	r := Rule{Kind: KindTCP, Port: httpPort}

	hostPort := pattern
	if i := strings.IndexByte(pattern, '/'); i >= 0 {
		r.Kind = KindHTTP
		hostPort, r.Path = pattern[:i], pattern[i:]
	}

	host, portStr, hasPort := strings.Cut(hostPort, ":")
	if hasPort {
		port, err := parsePort(portStr)
		if err != nil {
			return Rule{}, fmt.Errorf("rule %q: %w", pattern, err)
		}
		if r.Kind == KindHTTP && port != httpPort {
			return Rule{}, fmt.Errorf("rule %q: HTTP routes listen on port 80; drop the port", pattern)
		}
		r.Port = port
	}

	host = strings.ToLower(host)
	if err := validateHostname(host); err != nil {
		return Rule{}, fmt.Errorf("rule %q: %w", pattern, err)
	}
	r.Host = host

	ep, err := parseEndpoint(target)
	if err != nil {
		return Rule{}, fmt.Errorf("rule %q: %w", pattern, err)
	}
	r.Target = ep
	return r, nil
}

func parsePort(s string) (uint16, error) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil || n < 1 || n > 65535 {
		return 0, fmt.Errorf("invalid port %q (want 1-65535)", s)
	}
	return uint16(n), nil
}

func validateHostname(host string) error {
	if host == "" {
		return fmt.Errorf("missing hostname")
	}
	for _, label := range strings.Split(host, ".") {
		if !labelRe.MatchString(label) {
			return fmt.Errorf("invalid hostname %q (letters, digits, '-' and '.' only; no wildcards)", host)
		}
	}
	return nil
}

func parseEndpoint(s string) (Endpoint, error) {
	s = strings.TrimSpace(s)
	host, portStr, ok := strings.Cut(s, ":")
	if !ok {
		return Endpoint{}, fmt.Errorf("target %q: missing port (want name:port)", s)
	}
	port, err := parsePort(portStr)
	if err != nil {
		return Endpoint{}, fmt.Errorf("target %q: %w", s, err)
	}
	host = strings.ToLower(host)
	if _, err := netip.ParseAddr(host); err != nil && !labelRe.MatchString(host) {
		return Endpoint{}, fmt.Errorf("target %q: want a session name or IPv4 address", s)
	}
	return Endpoint{Host: host, Port: port}, nil
}

// Matches reports whether an HTTP request for host+path hits this rule. The
// Host header's port and the query string are ignored.
func (r Rule) Matches(host, path string) bool {
	if r.Kind != KindHTTP {
		return false
	}
	if h, _, ok := strings.Cut(host, ":"); ok {
		host = h
	}
	if i := strings.IndexByte(path, queryStart); i >= 0 {
		path = path[:i]
	}
	return strings.EqualFold(host, r.Host) && globMatch(r.Path, path)
}

// MatchHTTP returns the first rule, in order, that matches the request.
func MatchHTTP(rules []Rule, host, path string) (Rule, bool) {
	for _, r := range rules {
		if r.Matches(host, path) {
			return r, true
		}
	}
	return Rule{}, false
}

// globMatch matches s against pattern, where '*' matches any run of
// characters (including '/' and the empty string) and everything else is
// literal.
func globMatch(pattern, s string) bool {
	p, i := 0, 0
	starP, starI := -1, 0
	for i < len(s) {
		switch {
		case p < len(pattern) && pattern[p] == wildcard:
			starP, starI = p, i
			p++
		case p < len(pattern) && pattern[p] == s[i]:
			p++
			i++
		case starP >= 0:
			starI++
			p, i = starP+1, starI
		default:
			return false
		}
	}
	for p < len(pattern) && pattern[p] == wildcard {
		p++
	}
	return p == len(pattern)
}

// Shadow describes an HTTP rule that can never match because an earlier rule
// for the same host already catches everything it would.
type Shadow struct {
	Index int // the unreachable rule
	By    int // the earlier rule that covers it
}

// Shadowed lists unreachable HTTP rules. Rule i covers rule j when i's glob
// matches j's pattern text: i's literals can only line up with j's literals,
// so anything j matches, i matches too. The check is conservative (it may
// miss exotic overlaps) but never reports a false positive.
func Shadowed(rules []Rule) []Shadow {
	var out []Shadow
	for j, later := range rules {
		if later.Kind != KindHTTP {
			continue
		}
		for i := 0; i < j; i++ {
			earlier := rules[i]
			if earlier.Kind == KindHTTP && earlier.Host == later.Host && globMatch(earlier.Path, later.Path) {
				out = append(out, Shadow{Index: j, By: i})
				break
			}
		}
	}
	return out
}

// CheckConflicts rejects rule sets that cannot be served: two TCP mappings for
// the same host:port, or a TCP mapping on port 80 of a host that has HTTP
// routes (the router proxy owns that port).
func CheckConflicts(rules []Rule) error {
	tcp := map[string]Rule{}
	httpHosts := map[string]bool{}
	for _, r := range rules {
		if r.Kind == KindHTTP {
			httpHosts[r.Host] = true
		}
	}
	for _, r := range rules {
		if r.Kind != KindTCP {
			continue
		}
		key := r.Pattern()
		if prev, dup := tcp[key]; dup {
			return fmt.Errorf("conflict: %q and %q both map %s", prev, r, key)
		}
		tcp[key] = r
		if r.Port == httpPort && httpHosts[r.Host] {
			return fmt.Errorf("conflict: %q uses port 80 on %s, which has HTTP routes", r, r.Host)
		}
	}
	return nil
}
