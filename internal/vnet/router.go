// Package vnet implements sax virtual networks: named routers with their own
// IPv4 subnet, per-session virtual addresses, compose-style names and ordered
// routing rules (TCP name mappings and first-match HTTP path routes).
//
// Nothing here touches the host network. A router is plain state on disk; the
// interpose shim loaded into session processes reads the rendered tables
// (names, svc, ports/) and translates virtual addresses to real loopback
// sockets, so it needs neither root nor virtualization. Two routers may use
// the same subnet: they never see each other.
package vnet

import (
	"fmt"
	"net/netip"
	"sort"
	"strings"
)

// Router is one virtual network. Values are immutable: every With*/Without*
// method returns a new Router and leaves its receiver untouched.
type Router struct {
	Name     string            `json:"name"`
	Subnet   string            `json:"subnet"`
	Domain   string            `json:"domain,omitempty"`
	Sessions map[string]string `json:"sessions"`       // session name -> virtual IP
	VIPs     map[string]string `json:"vips"`           // rule hostname -> service VIP
	Rules    []string          `json:"rules"`          // canonical rules, in match order
	Owners   map[string]string `json:"owners"`         // session name -> sax session running it
	Publish  []string          `json:"publish"`        // host port forwards, "15432 -> db:5432"
	View     string            `json:"view,omitempty"` // sax session showing one tab per app
	Dirs     map[string]string `json:"dirs,omitempty"` // session name -> its folder (for the banner)
	Banner   Banner            `json:"banner,omitempty"`
	// Source is the env file the router was last brought up from, and
	// Project its project there, so sax net finds it from any folder.
	Source  string `json:"source,omitempty"`
	Project string `json:"project,omitempty"`
}

// DefaultDomain is the domain of route hostnames written without one.
const DefaultDomain = "lan"

// NewRouter validates and builds an empty router; domain "" means
// DefaultDomain.
func NewRouter(name, subnet, domain string) (Router, error) {
	if domain == "" {
		domain = DefaultDomain
	}
	r := Router{
		Name:     name,
		Subnet:   subnet,
		Domain:   strings.ToLower(domain),
		Sessions: map[string]string{},
		VIPs:     map[string]string{},
		Rules:    []string{},
		Owners:   map[string]string{},
		Publish:  []string{},
	}
	if err := r.validateHeader(); err != nil {
		return Router{}, err
	}
	p, _ := ParseSubnet(subnet)
	r.Subnet = p.String()
	return r, nil
}

func (r Router) validateHeader() error {
	if err := ValidateName("router", r.Name); err != nil {
		return err
	}
	if _, err := ParseSubnet(r.Subnet); err != nil {
		return fmt.Errorf("router %s: %w", r.Name, err)
	}
	if r.Domain != "" {
		if err := validateHostname(r.Domain); err != nil {
			return fmt.Errorf("router %s: domain: %w", r.Name, err)
		}
	}
	return nil
}

// ValidateName checks router and session names: they double as hostnames, so
// they must be valid DNS labels.
func ValidateName(kind, name string) error {
	if !labelRe.MatchString(name) {
		return fmt.Errorf("invalid %s name %q (lowercase letters, digits and '-', max 63)", kind, name)
	}
	return nil
}

func (r Router) prefix() netip.Prefix {
	p, _ := ParseSubnet(r.Subnet)
	return p
}

func (r Router) clone() Router {
	out := r
	out.Sessions = make(map[string]string, len(r.Sessions))
	for k, v := range r.Sessions {
		out.Sessions[k] = v
	}
	out.VIPs = make(map[string]string, len(r.VIPs))
	for k, v := range r.VIPs {
		out.VIPs[k] = v
	}
	out.Rules = append([]string{}, r.Rules...)
	out.Publish = append([]string{}, r.Publish...)
	out.Dirs = make(map[string]string, len(r.Dirs))
	for k, v := range r.Dirs {
		out.Dirs[k] = v
	}
	out.Owners = make(map[string]string, len(r.Owners))
	for k, v := range r.Owners {
		out.Owners[k] = v
	}
	return out
}

// usedIPs returns every address taken by a session or a service VIP.
func (r Router) usedIPs() map[string]bool {
	used := make(map[string]bool, len(r.Sessions)+len(r.VIPs))
	for _, ip := range r.Sessions {
		used[ip] = true
	}
	for _, ip := range r.VIPs {
		used[ip] = true
	}
	return used
}

// WithSession assigns name an address. ip "" picks the lowest free address;
// an existing session keeps its address (so restarts are stable). Returns the
// new router and the session's address.
func (r Router) WithSession(name, ip string) (Router, string, error) {
	if err := ValidateName("session", name); err != nil {
		return r, "", err
	}
	if cur, ok := r.Sessions[name]; ok {
		if ip != "" && ip != cur {
			return r, "", fmt.Errorf("session %s already has %s in router %s", name, cur, r.Name)
		}
		return r, cur, nil
	}

	p := r.prefix()
	used := r.usedIPs()
	if ip == "" {
		free, err := r.lowestFree(p, used)
		if err != nil {
			return r, "", err
		}
		ip = free
	} else if err := r.checkAssignable(p, used, ip); err != nil {
		return r, "", err
	}

	out := r.clone()
	out.Sessions[name] = ip
	return out, ip, nil
}

func (r Router) checkAssignable(p netip.Prefix, used map[string]bool, ip string) error {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return fmt.Errorf("invalid ip %q", ip)
	}
	if !usableHost(p, addr) {
		return fmt.Errorf("ip %s is not assignable in %s (network, gateway .%d and broadcast are reserved)", ip, p, gatewayIndex)
	}
	if used[addr.String()] {
		return fmt.Errorf("ip %s is already in use in router %s", ip, r.Name)
	}
	return nil
}

func (r Router) lowestFree(p netip.Prefix, used map[string]bool) (string, error) {
	for n := uint32(firstSessionIndex); n <= hostCount(p); n++ {
		if a := hostAt(p, n).String(); !used[a] {
			return a, nil
		}
	}
	return "", fmt.Errorf("router %s: subnet %s is full", r.Name, p)
}

func (r Router) highestFree(p netip.Prefix, used map[string]bool) (string, error) {
	for n := hostCount(p); n >= firstSessionIndex; n-- {
		if a := hostAt(p, n).String(); !used[a] {
			return a, nil
		}
	}
	return "", fmt.Errorf("router %s: subnet %s is full", r.Name, p)
}

// WithoutSession forgets a session's address and owner.
func (r Router) WithoutSession(name string) Router {
	out := r.clone()
	delete(out.Sessions, name)
	delete(out.Owners, name)
	delete(out.Dirs, name)
	return out
}

// WithDir records the folder a router session runs in ("" forgets it).
func (r Router) WithDir(name, dir string) Router {
	out := r.clone()
	if dir == "" {
		delete(out.Dirs, name)
	} else {
		out.Dirs[name] = dir
	}
	return out
}

// WithBanner sets the router's page banner.
func (r Router) WithBanner(b Banner) Router {
	out := r.clone()
	out.Banner = b
	return out
}

// WithSource records the env file and project the router comes from.
func (r Router) WithSource(path, project string) Router {
	out := r.clone()
	out.Source, out.Project = path, project
	return out
}

// WithView records the sax session that shows this router's apps as tabs
// ("" when there is none).
func (r Router) WithView(session string) Router {
	out := r.clone()
	out.View = session
	return out
}

// WithOwner records which sax session runs a router session, so `sax net
// down` can stop it.
func (r Router) WithOwner(name, saxSession string) Router {
	out := r.clone()
	out.Owners[name] = saxSession
	return out
}

// ParsedRules parses the stored rules in order.
func (r Router) ParsedRules() ([]Rule, error) {
	rules := make([]Rule, 0, len(r.Rules))
	for _, s := range r.Rules {
		rule, err := ParseRule(s)
		if err != nil {
			return nil, fmt.Errorf("router %s: %w", r.Name, err)
		}
		rules = append(rules, rule)
	}
	return rules, nil
}

// WithRule appends a rule (it is evaluated after all existing ones) and gives
// its hostname a service VIP, allocated from the top of the subnet down so it
// stays clear of session addresses. A hostname without a dot gets the
// router's domain: "bob/api/*" is served as bob.lan.
func (r Router) WithRule(raw string) (Router, error) {
	rule, err := ParseRule(raw)
	if err != nil {
		return r, err
	}
	rule = rule.InDomain(r.Domain)
	existing, err := r.ParsedRules()
	if err != nil {
		return r, err
	}
	if err := CheckConflicts(append(existing, rule)); err != nil {
		return r, err
	}

	out := r.clone()
	out.Rules = append(out.Rules, rule.String())
	if _, ok := out.VIPs[rule.Host]; !ok {
		vip, err := r.highestFree(r.prefix(), r.usedIPs())
		if err != nil {
			return r, err
		}
		out.VIPs[rule.Host] = vip
	}
	return out, nil
}

// WithoutRule removes the first rule whose pattern equals pattern and releases
// the hostname's VIP once no rule uses it.
func (r Router) WithoutRule(pattern string) (Router, error) {
	rules, err := r.ParsedRules()
	if err != nil {
		return r, err
	}
	want := strings.ToLower(strings.TrimSpace(pattern))
	for i, rule := range rules {
		if rule.Pattern() != want {
			continue
		}
		out := r.clone()
		out.Rules = append(append([]string{}, r.Rules[:i]...), r.Rules[i+1:]...)
		if !hostInUse(append(append([]Rule{}, rules[:i]...), rules[i+1:]...), rule.Host) {
			delete(out.VIPs, rule.Host)
		}
		return out, nil
	}
	return r, fmt.Errorf("router %s: no rule for %q", r.Name, pattern)
}

func hostInUse(rules []Rule, host string) bool {
	for _, r := range rules {
		if r.Host == host {
			return true
		}
	}
	return false
}

// Resolve turns a rule target into a virtual address, if it is known.
func (r Router) Resolve(ep Endpoint) (string, bool) {
	if addr, err := netip.ParseAddr(ep.Host); err == nil {
		return addr.String(), usableHost(r.prefix(), addr)
	}
	ip, ok := r.Sessions[ep.Host]
	return ip, ok
}

// SortedSessions returns session names in a stable order.
func (r Router) SortedSessions() []string {
	names := make([]string, 0, len(r.Sessions))
	for n := range r.Sessions {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
