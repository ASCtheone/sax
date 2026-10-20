package vnet

import (
	"fmt"
	"net/netip"
	"strings"
)

const (
	minPrefixBits = 8
	maxPrefixBits = 30
	// gatewayIndex is the host index reserved for the router itself.
	gatewayIndex = 1
	// firstSessionIndex is where automatic session addresses start.
	firstSessionIndex = 2
)

// ParseSubnet parses an IPv4 CIDR for a router. The address must be the
// network address (no host bits) and the prefix must leave room for sessions.
func ParseSubnet(s string) (netip.Prefix, error) {
	p, err := netip.ParsePrefix(strings.TrimSpace(s))
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("invalid subnet %q: want CIDR like 10.12.12.0/24", s)
	}
	if !p.Addr().Is4() {
		return netip.Prefix{}, fmt.Errorf("invalid subnet %q: only IPv4 is supported", s)
	}
	if p.Bits() < minPrefixBits || p.Bits() > maxPrefixBits {
		return netip.Prefix{}, fmt.Errorf("invalid subnet %q: prefix must be /%d to /%d", s, minPrefixBits, maxPrefixBits)
	}
	if p.Masked() != p {
		return netip.Prefix{}, fmt.Errorf("invalid subnet %q: host bits set (did you mean %s?)", s, p.Masked())
	}
	return p, nil
}

// hostCount is the number of assignable host indexes (excludes network and
// broadcast addresses).
func hostCount(p netip.Prefix) uint32 {
	return (uint32(1) << (32 - p.Bits())) - 2
}

// hostAt returns the address at host index n within p.
func hostAt(p netip.Prefix, n uint32) netip.Addr {
	b := p.Addr().As4()
	base := uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
	v := base + n
	return netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)})
}

// indexOf returns the host index of addr within p. The caller must ensure
// p.Contains(addr).
func indexOf(p netip.Prefix, addr netip.Addr) uint32 {
	a, b := addr.As4(), p.Addr().As4()
	av := uint32(a[0])<<24 | uint32(a[1])<<16 | uint32(a[2])<<8 | uint32(a[3])
	bv := uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
	return av - bv
}

// usableHost reports whether addr can be assigned to a session or service:
// inside p, not the network, broadcast or gateway address.
func usableHost(p netip.Prefix, addr netip.Addr) bool {
	if !p.Contains(addr) {
		return false
	}
	n := indexOf(p, addr)
	return n > gatewayIndex && n <= hostCount(p)
}
