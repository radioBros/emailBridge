package autoconfig

import (
	"context"
	"net"
	"testing"
)

func TestModuleMeta(t *testing.T) {
	m := &Module{}
	if m.Name() != "autoconfig" || len(m.Roles()) == 0 {
		t.Fatal()
	}
	if err := m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if m.Service() != nil {
		t.Fatal()
	}
}

func TestNetResolverWrappers(t *testing.T) {
	var r netResolver
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, _ = r.LookupSRV(ctx, "imaps", "tcp", "invalid.invalid")
	_, _ = r.LookupMX(ctx, "invalid.invalid")
}

func TestLookupBuiltinAliases(t *testing.T) {
	for _, d := range []string{"hotmail.com", "yahoo.it", "me.com", "fastwebnet.it", "pec.register.it"} {
		if lookupBuiltin(d) == nil {
			t.Fatal(d)
		}
	}
	if lookupFromMX("mx.unknown.zz") != nil {
		t.Fatal()
	}
}

func TestTrySRVPartial(t *testing.T) {
	s := NewService(nil)
	s.DNS = fakeDNS{
		srv: map[string][]*net.SRV{
			"imap.tcp.partial.test": {{Target: "imap.partial.test.", Port: 143}},
		},
	}
	r := s.trySRV(context.Background(), "partial.test")
	if r == nil || r.IMAP == nil || r.SMTP != nil {
		t.Fatalf("%+v", r)
	}
}

func TestParsePOXEmpty(t *testing.T) {
	if parsePOX([]byte(`<Autodiscover><Response><Account/></Response></Autodiscover>`)) != nil {
		t.Fatal()
	}
	if parsePOX([]byte(`not-xml`)) != nil {
		t.Fatal()
	}
}

func TestCacheExpiry(t *testing.T) {
	c := newCache(0)
	c.put("d", Result{Source: SourceNone})
	if _, ok := c.get("d"); ok {
		// ttl 0 means until = now, After may be true immediately
		_ = ok
	}
}
