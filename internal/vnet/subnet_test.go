package vnet

import (
	"net/netip"
	"testing"
)

func TestParseSubnet(t *testing.T) {
	good := map[string]string{
		"10.12.12.0/24":    "10.12.12.0/24",
		"10.77.0.0/16":     "10.77.0.0/16",
		" 192.168.5.0/29 ": "192.168.5.0/29",
	}
	for in, want := range good {
		p, err := ParseSubnet(in)
		if err != nil {
			t.Errorf("ParseSubnet(%q) error: %v", in, err)
			continue
		}
		if p.String() != want {
			t.Errorf("ParseSubnet(%q) = %s, want %s", in, p, want)
		}
	}

	bad := []string{
		"",
		"10.12.12.0",      // no prefix length
		"10.12.12.4/24",   // host bits set
		"10.0.0.0/7",      // too large
		"10.12.12.0/31",   // too small for sessions
		"fd00::/64",       // IPv6
		"not-a-subnet/24", // garbage
	}
	for _, in := range bad {
		if _, err := ParseSubnet(in); err == nil {
			t.Errorf("ParseSubnet(%q) expected error", in)
		}
	}
}

func TestHostAtAndIndexOf(t *testing.T) {
	p := netip.MustParsePrefix("10.12.12.0/24")
	if got := hostCount(p); got != 254 {
		t.Fatalf("hostCount = %d, want 254", got)
	}
	if got := hostAt(p, 4); got != netip.MustParseAddr("10.12.12.4") {
		t.Errorf("hostAt(4) = %s", got)
	}
	if got := hostAt(p, 254); got != netip.MustParseAddr("10.12.12.254") {
		t.Errorf("hostAt(254) = %s", got)
	}
	if got := indexOf(p, netip.MustParseAddr("10.12.12.200")); got != 200 {
		t.Errorf("indexOf(.200) = %d", got)
	}
}

func TestUsableHost(t *testing.T) {
	p := netip.MustParsePrefix("10.12.12.0/24")
	cases := map[string]bool{
		"10.12.12.0":   false, // network
		"10.12.12.1":   false, // reserved gateway
		"10.12.12.2":   true,
		"10.12.12.254": true,
		"10.12.12.255": false, // broadcast
		"10.12.13.4":   false, // outside
	}
	for ip, want := range cases {
		if got := usableHost(p, netip.MustParseAddr(ip)); got != want {
			t.Errorf("usableHost(%s) = %v, want %v", ip, got, want)
		}
	}
}
