package vnet

import (
	"fmt"
	"sort"
	"strings"
)

// NameEntry maps a hostname to a virtual address.
type NameEntry struct {
	Name string
	IP   string
}

// ServiceEntry maps a service VIP:port to a resolved target address.
type ServiceEntry struct {
	VIP        string
	Port       uint16
	TargetIP   string
	TargetPort uint16
}

// HTTPHost is a hostname whose port 80 is served by the router's HTTP proxy.
type HTTPHost struct {
	Host string
	VIP  string
}

// Tables is everything the interpose shim (and the HTTP proxy) needs, rendered
// from a Router.
type Tables struct {
	Names    []NameEntry
	Services []ServiceEntry
	HTTP     []HTTPHost
}

// NamesFile renders the shim's "names" file: "<name> <ip>" per line.
func (t Tables) NamesFile() string {
	var b strings.Builder
	for _, n := range t.Names {
		fmt.Fprintf(&b, "%s %s\n", n.Name, n.IP)
	}
	return b.String()
}

// ServicesFile renders the shim's "svc" file: "<vip> <port> <ip> <port>".
func (t Tables) ServicesFile() string {
	var b strings.Builder
	for _, s := range t.Services {
		fmt.Fprintf(&b, "%s %d %s %d\n", s.VIP, s.Port, s.TargetIP, s.TargetPort)
	}
	return b.String()
}

// Tables renders the router's lookup tables. Rules whose target is not
// resolvable yet (session not started) are left out and reported as warnings
// along with HTTP rules that can never match.
func (r Router) Tables() (Tables, []string) {
	var t Tables
	var warnings []string

	for _, name := range r.SortedSessions() {
		ip := r.Sessions[name]
		t.Names = append(t.Names, NameEntry{Name: name, IP: ip})
		if r.Domain != "" {
			t.Names = append(t.Names, NameEntry{Name: name + "." + r.Domain, IP: ip})
		}
	}

	hosts := make([]string, 0, len(r.VIPs))
	for h := range r.VIPs {
		hosts = append(hosts, h)
	}
	sort.Strings(hosts)
	for _, h := range hosts {
		t.Names = append(t.Names, NameEntry{Name: h, IP: r.VIPs[h]})
	}

	rules, err := r.ParsedRules()
	if err != nil {
		return t, append(warnings, err.Error())
	}

	httpSeen := map[string]bool{}
	for _, rule := range rules {
		if rule.Kind == KindHTTP {
			if !httpSeen[rule.Host] {
				httpSeen[rule.Host] = true
				t.HTTP = append(t.HTTP, HTTPHost{Host: rule.Host, VIP: r.VIPs[rule.Host]})
			}
			if _, ok := r.resolve(rule.Target); !ok {
				warnings = append(warnings, unresolvedWarning(rule))
			}
			continue
		}
		ip, ok := r.resolve(rule.Target)
		if !ok {
			warnings = append(warnings, unresolvedWarning(rule))
			continue
		}
		t.Services = append(t.Services, ServiceEntry{
			VIP: r.VIPs[rule.Host], Port: rule.Port, TargetIP: ip, TargetPort: rule.Target.Port,
		})
	}

	for _, s := range Shadowed(rules) {
		warnings = append(warnings, fmt.Sprintf(
			"rule %q never matches: earlier rule %q already catches it (rules are first-match)",
			rules[s.Index], rules[s.By]))
	}
	return t, warnings
}

func unresolvedWarning(rule Rule) string {
	return fmt.Sprintf("rule %q: target %s is not running in this router yet", rule, rule.Target.Host)
}
