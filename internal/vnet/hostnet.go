package vnet

import (
	"fmt"
	"net"
	"net/netip"
)

// autoSubnetBase and autoSubnetCount bound the 10.77.N.0/24 ranges handed out
// when the user does not choose a subnet.
const (
	autoSubnetBase  = "10.77.%d.0/24"
	autoSubnetCount = 256
)

// HostOverlaps lists host interface networks that overlap p. Sessions in such
// a router would no longer reach those real machines, because the shim treats
// the whole subnet as virtual.
func HostOverlaps(p netip.Prefix) []string {
	return overlaps(p, hostPrefixes())
}

// PickSubnet returns a free 10.77.N.0/24 that no router uses and no host
// interface overlaps. Routers may share subnets, but distinct defaults keep
// `sax net ls` easy to read.
func PickSubnet(routers []Router) (string, error) {
	return pickSubnet(routers, hostPrefixes())
}

func pickSubnet(routers []Router, host []netip.Prefix) (string, error) {
	taken := make(map[string]bool, len(routers))
	for _, r := range routers {
		taken[r.Subnet] = true
	}
	for n := 0; n < autoSubnetCount; n++ {
		cand := fmt.Sprintf(autoSubnetBase, n)
		if taken[cand] {
			continue
		}
		if len(overlaps(netip.MustParsePrefix(cand), host)) == 0 {
			return cand, nil
		}
	}
	return "", fmt.Errorf("no free 10.77.x.0/24 subnet; pass --subnet")
}

func overlaps(p netip.Prefix, host []netip.Prefix) []string {
	var out []string
	for _, h := range host {
		if h.Addr().Is4() && !h.Addr().IsLoopback() && h.Masked().Overlaps(p) {
			out = append(out, h.String())
		}
	}
	return out
}

func hostPrefixes() []netip.Prefix {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil // best effort: overlap detection is advisory only
	}
	out := make([]netip.Prefix, 0, len(addrs))
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok {
			if p, err := netip.ParsePrefix(ipnet.String()); err == nil {
				out = append(out, p)
			}
		}
	}
	return out
}
