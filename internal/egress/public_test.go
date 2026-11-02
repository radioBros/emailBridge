package egress

import (
	"context"
	"net/netip"
	"testing"
)

type fixedResolver map[string][]netip.Addr

func (f fixedResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	return f[host], nil
}

func TestPublicOnlyFollowsParentAndBlocksPrivate(t *testing.T) {
	parent := NewDialer(Policy{})
	parent.Resolver = fixedResolver{"intranet.test": {netip.MustParseAddr("10.0.0.5")}, "pub.test": {netip.MustParseAddr("93.184.216.34")}}
	pub := parent.PublicOnly()
	if p := pub.Policy(); !p.BlockPrivate || !p.BlockLinkLocal {
		t.Fatalf("public-only policy: %+v", p)
	}
	if p := parent.Policy(); p.BlockPrivate {
		t.Fatal("the parent keeps its own policy")
	}
	// Runtime changes and the allow-list of the parent apply to the view.
	parent.SetPolicy(Policy{AllowHosts: []string{"intranet.test"}})
	if got := pub.Policy().AllowHosts; len(got) != 1 || got[0] != "intranet.test" {
		t.Fatalf("allow hosts not followed: %v", got)
	}
	if !pub.hostAllowed("intranet.test") {
		t.Fatal("an allow-listed host stays reachable")
	}
}
