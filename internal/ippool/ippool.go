// Package ippool selects a local bind address for IMAP/SMTP dials (W57 / G25).
package ippool

import (
	"hash/fnv"
	"log/slog"
	"net"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"
)

// Strategies for picking among localAddresses.
const (
	StrategyRandom        = "random"
	StrategyRoundRobin    = "round-robin"
	StrategyHashByAccount = "hash-by-account"
)

// DefaultStrategy when unset.
const DefaultStrategy = StrategyRoundRobin

// Config is one organisation (or account-override) pool.
type Config struct {
	LocalAddresses []string
	SMTPStrategy   string
	IMAPStrategy   string
}

// NormalizeStrategy returns a known strategy or DefaultStrategy.
func NormalizeStrategy(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case StrategyRandom, StrategyRoundRobin, StrategyHashByAccount:
		return strings.ToLower(strings.TrimSpace(s))
	default:
		return DefaultStrategy
	}
}

// FilterBound keeps only addresses that exist on a local interface. Invalid / unbound
// entries are returned in skipped (for logging).
func FilterBound(addrs []string) (ok, skipped []string) {
	bound := localIPs()
	for _, raw := range addrs {
		ip := parseIP(raw)
		if ip == "" {
			skipped = append(skipped, raw)
			continue
		}
		if !bound[ip] {
			skipped = append(skipped, raw)
			continue
		}
		ok = append(ok, ip)
	}
	return ok, skipped
}

func parseIP(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(s); err == nil {
		s = host
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return ""
	}
	return a.String()
}

func localIPs() map[string]bool {
	out := map[string]bool{}
	ifaces, err := net.Interfaces()
	if err != nil {
		return out
	}
	for _, iface := range ifaces {
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip == nil {
				continue
			}
			if x, ok := netip.AddrFromSlice(ip); ok {
				out[x.Unmap().String()] = true
			}
		}
	}
	return out
}

// Selector picks local addresses and records metrics.
type Selector struct {
	Log     *slog.Logger
	Picks   *prometheus.CounterVec // labels: address, protocol
	randFn  func(n int) int        // tests; nil = crypto/math via atomic fallback
	mu      sync.Mutex
	rr      map[string]*atomic.Uint64 // key orgID|proto
	randCtr atomic.Uint64
}

// NewSelector returns a selector with optional metrics.
func NewSelector(log *slog.Logger, picks *prometheus.CounterVec) *Selector {
	if log == nil {
		log = slog.Default()
	}
	return &Selector{Log: log, Picks: picks, rr: map[string]*atomic.Uint64{}}
}

// ResolveLocal returns the bind address for a dial.
// accountLocal (singular) wins; otherwise pool pick from addrs + strategy.
func (s *Selector) ResolveLocal(orgID, accountID, protocol, accountLocal string, addrs []string, strategy string) string {
	if a := parseIP(accountLocal); a != "" {
		s.inc(a, protocol)
		return a
	}
	bound, skipped := FilterBound(addrs)
	for _, sk := range skipped {
		s.Log.Warn("ip pool address skipped (not bound or invalid)",
			"address", sk, "organization_id", orgID, "protocol", protocol)
	}
	if len(bound) == 0 {
		return ""
	}
	strategy = NormalizeStrategy(strategy)
	var pick string
	switch strategy {
	case StrategyRandom:
		pick = bound[s.randIndex(len(bound))]
	case StrategyHashByAccount:
		h := fnv.New32a()
		_, _ = h.Write([]byte(accountID))
		pick = bound[int(h.Sum32())%len(bound)]
	default: // round-robin
		c := s.counter(orgID, protocol)
		idx := (c.Add(1) - 1) % uint64(len(bound))
		pick = bound[int(idx)] //nolint:gosec // G115: idx < len(bound)
	}
	s.inc(pick, protocol)
	return pick
}

func (s *Selector) counter(orgID, protocol string) *atomic.Uint64 {
	key := orgID + "|" + protocol
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rr == nil {
		s.rr = map[string]*atomic.Uint64{}
	}
	c, ok := s.rr[key]
	if !ok {
		c = &atomic.Uint64{}
		s.rr[key] = c
	}
	return c
}

func (s *Selector) randIndex(n int) int {
	if n <= 1 {
		return 0
	}
	if s.randFn != nil {
		return s.randFn(n)
	}
	idx := (s.randCtr.Add(1) - 1) % uint64(n)
	return int(idx) //nolint:gosec // G115: idx < n
}

func (s *Selector) inc(addr, protocol string) {
	if s == nil || s.Picks == nil || addr == "" {
		return
	}
	s.Picks.WithLabelValues(addr, protocol).Inc()
}

// NewPicksCounter builds the picks metric (register with the process registry).
func NewPicksCounter() *prometheus.CounterVec {
	return prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "eb_ippool_picks_total",
		Help: "Outbound IP pool local-address picks by address and protocol (imap|smtp).",
	}, []string{"address", "protocol"})
}
