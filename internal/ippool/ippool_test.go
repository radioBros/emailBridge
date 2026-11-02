package ippool

import (
	"context"
	"log/slog"
	"testing"

	"github.com/radiobros/emailbridge/internal/app"
)

func TestNormalizeStrategy(t *testing.T) {
	if NormalizeStrategy("") != DefaultStrategy {
		t.Fatal("default")
	}
	if NormalizeStrategy("RANDOM") != StrategyRandom {
		t.Fatal("random")
	}
	if NormalizeStrategy("nope") != DefaultStrategy {
		t.Fatal("fallback")
	}
}

func TestFilterBoundLoopback(t *testing.T) {
	ok, skipped := FilterBound([]string{"127.0.0.1", "not-an-ip", "203.0.113.1"})
	if len(ok) == 0 {
		t.Skip("127.0.0.1 not bound on this host")
	}
	found := false
	for _, a := range ok {
		if a == "127.0.0.1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected 127.0.0.1 in %v", ok)
	}
	if len(skipped) < 1 {
		t.Fatalf("expected skips, got %v", skipped)
	}
}

func TestResolveLocalSingularWins(t *testing.T) {
	s := NewSelector(slog.Default(), NewPicksCounter())
	got := s.ResolveLocal("org", "acc_1", "imap", "127.0.0.1", []string{"127.0.0.2"}, StrategyRoundRobin)
	if got != "127.0.0.1" {
		t.Fatalf("got %q", got)
	}
}

func TestResolveLocalHashStable(t *testing.T) {
	ok, _ := FilterBound([]string{"127.0.0.1"})
	if len(ok) == 0 {
		t.Skip("127.0.0.1 not bound")
	}
	// Two distinct loopbacks only on some Windows setups.
	addrs := []string{"127.0.0.1"}
	if extra, _ := FilterBound([]string{"127.0.0.2"}); len(extra) > 0 {
		addrs = append(addrs, "127.0.0.2")
	}
	s := NewSelector(slog.Default(), nil)
	a := s.ResolveLocal("org", "acc_stable", "imap", "", addrs, StrategyHashByAccount)
	b := s.ResolveLocal("org", "acc_stable", "imap", "", addrs, StrategyHashByAccount)
	if a == "" || a != b {
		t.Fatalf("unstable %q vs %q", a, b)
	}
}

func TestResolveLocalRoundRobin(t *testing.T) {
	ok, _ := FilterBound([]string{"127.0.0.1"})
	if len(ok) == 0 {
		t.Skip("127.0.0.1 not bound")
	}
	addrs := ok
	if extra, _ := FilterBound([]string{"127.0.0.2"}); len(extra) > 0 {
		addrs = append(addrs, extra...)
	}
	s := NewSelector(slog.Default(), NewPicksCounter())
	first := s.ResolveLocal("orgRR", "a1", "smtp", "", addrs, StrategyRoundRobin)
	second := s.ResolveLocal("orgRR", "a2", "smtp", "", addrs, StrategyRoundRobin)
	if first == "" || second == "" {
		t.Fatal("empty pick")
	}
	if len(addrs) > 1 && first == second {
		// round-robin should advance; with 2+ addrs consecutive picks differ
		t.Fatalf("expected different consecutive picks, got %q twice", first)
	}
}

func TestResolveLocalEmpty(t *testing.T) {
	s := NewSelector(slog.Default(), nil)
	if got := s.ResolveLocal("o", "a", "smtp", "", nil, ""); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestParseIPWithPort(t *testing.T) {
	if parseIP("127.0.0.1:25") != "127.0.0.1" {
		t.Fatal(parseIP("127.0.0.1:25"))
	}
	if parseIP("bogus") != "" {
		t.Fatal("expected empty")
	}
}

func TestResolveLocalRandom(t *testing.T) {
	ok, _ := FilterBound([]string{"127.0.0.1"})
	if len(ok) == 0 {
		t.Skip("127.0.0.1 not bound")
	}
	s := NewSelector(nil, nil) // exercises default log
	got := s.ResolveLocal("org", "acc", "imap", "", ok, StrategyRandom)
	if got != "127.0.0.1" {
		t.Fatalf("got %q", got)
	}
	if s.randIndex(1) != 0 {
		t.Fatal("randIndex n=1")
	}
	_ = s.randIndex(3)
}

func TestModuleLifecycle(t *testing.T) {
	m := &Module{}
	if m.Name() != "ippool" {
		t.Fatal(m.Name())
	}
	if len(m.Roles()) < 1 {
		t.Fatal("roles")
	}
	a := app.New(app.Options{Log: slog.Default()})
	if err := m.Start(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if _, ok := app.Get[*Selector](a); !ok {
		t.Fatal("selector not provided")
	}
	if err := m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}
