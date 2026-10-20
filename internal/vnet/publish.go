package vnet

import (
	"fmt"
	"strings"
)

// Publish forwards a host port (127.0.0.1:HostPort) into the router, so tools
// outside sax (a DB client, a browser) can reach a session.
type Publish struct {
	HostPort uint16
	Target   Endpoint
}

func (p Publish) String() string { return fmt.Sprintf("%d %s %s", p.HostPort, ruleArrow, p.Target) }

// ParsePublish parses "15432 -> db:5432".
func ParsePublish(s string) (Publish, error) {
	left, right, ok := strings.Cut(s, ruleArrow)
	if !ok {
		return Publish{}, fmt.Errorf("publish %q: want \"<host-port> %s <target>\"", s, ruleArrow)
	}
	port, err := parsePort(left)
	if err != nil {
		return Publish{}, fmt.Errorf("publish %q: host %w", s, err)
	}
	ep, err := parseEndpoint(right)
	if err != nil {
		return Publish{}, fmt.Errorf("publish %q: %w", s, err)
	}
	return Publish{HostPort: port, Target: ep}, nil
}

// ParsedPublish parses the stored publishes in order.
func (r Router) ParsedPublish() ([]Publish, error) {
	out := make([]Publish, 0, len(r.Publish))
	for _, s := range r.Publish {
		p, err := ParsePublish(s)
		if err != nil {
			return nil, fmt.Errorf("router %s: %w", r.Name, err)
		}
		out = append(out, p)
	}
	return out, nil
}

// WithPublish adds a host port forward. Host ports are unique per router;
// clashes across routers are reported by the gateway when it binds.
func (r Router) WithPublish(raw string) (Router, error) {
	p, err := ParsePublish(raw)
	if err != nil {
		return r, err
	}
	existing, err := r.ParsedPublish()
	if err != nil {
		return r, err
	}
	for _, e := range existing {
		if e.HostPort == p.HostPort {
			return r, fmt.Errorf("host port %d is already published as %q", p.HostPort, e)
		}
	}
	out := r.clone()
	out.Publish = append(out.Publish, p.String())
	return out, nil
}

// WithoutPublish removes the forward for a host port.
func (r Router) WithoutPublish(hostPort string) (Router, error) {
	port, err := parsePort(hostPort)
	if err != nil {
		return r, err
	}
	existing, err := r.ParsedPublish()
	if err != nil {
		return r, err
	}
	for i, e := range existing {
		if e.HostPort == port {
			out := r.clone()
			out.Publish = append(append([]string{}, r.Publish[:i]...), r.Publish[i+1:]...)
			return out, nil
		}
	}
	return r, fmt.Errorf("router %s: host port %d is not published", r.Name, port)
}
