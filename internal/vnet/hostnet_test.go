package vnet

import (
	"net/netip"
	"reflect"
	"testing"
)

func TestOverlaps(t *testing.T) {
	host := []netip.Prefix{
		netip.MustParsePrefix("192.168.1.20/24"),
		netip.MustParsePrefix("10.12.0.5/16"),
		netip.MustParsePrefix("127.0.0.1/8"),
		netip.MustParsePrefix("fe80::1/64"),
	}
	got := overlaps(netip.MustParsePrefix("10.12.12.0/24"), host)
	if !reflect.DeepEqual(got, []string{"10.12.0.5/16"}) {
		t.Errorf("overlaps = %v", got)
	}
	if got := overlaps(netip.MustParsePrefix("10.77.0.0/24"), host); len(got) != 0 {
		t.Errorf("expected no overlap, got %v", got)
	}
}

func TestPickSubnet(t *testing.T) {
	host := []netip.Prefix{netip.MustParsePrefix("10.77.1.9/24")}
	routers := []Router{{Subnet: "10.77.0.0/24"}}
	got, err := pickSubnet(routers, host)
	if err != nil {
		t.Fatal(err)
	}
	if got != "10.77.2.0/24" { // .0 used by a router, .1 by the host
		t.Errorf("pickSubnet = %s, want 10.77.2.0/24", got)
	}
}
