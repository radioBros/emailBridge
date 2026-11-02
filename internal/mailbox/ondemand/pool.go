// Package ondemand is the per-account on-demand IMAP connection pool used by the mailbox/messages
// API on api nodes (S5 §3, P4.1): at most one connection per account, idle-closed after 60 s.
package ondemand

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/ippool"
	"github.com/radiobros/emailbridge/internal/mailproto/imapc"
	ebsync "github.com/radiobros/emailbridge/internal/sync"
)

// ErrBusy means the account's single on-demand slot is already in use.
var ErrBusy = errors.New("ondemand: connection busy")

const defaultIdle = 60 * time.Second

// Pool keeps at most one on-demand IMAP connection per account (S5 §3).
type Pool struct {
	Dialer *egress.Dialer
	IPPool *ippool.Selector
	// IMAPID is the global EB_IMAP_ID_* map; account IMAPID overlays it (W57).
	IMAPID map[string]string
	Idle   time.Duration
	// Metrics may be nil (tests); production registers via RegisterMetrics.
	Metrics *Metrics

	mu     sync.Mutex
	byAcct map[string]*slot
}

type slot struct {
	sem    chan struct{}
	mu     sync.Mutex
	client *imapc.Client
	since  time.Time
	addr   string
}

// Lease is one borrowed on-demand IMAP connection.
type Lease struct {
	Client *imapc.Client
	pool   *Pool
	slot   *slot
	done   bool
}

// acquireWait bounds how long a request waits for the account's busy on-demand connection.
const acquireWait = 10 * time.Second

func (p *Pool) idleFor() time.Duration {
	if p.Idle > 0 {
		return p.Idle
	}
	return defaultIdle
}

func (p *Pool) slotOf(accountID string) *slot {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.byAcct == nil {
		p.byAcct = map[string]*slot{}
	}
	s, ok := p.byAcct[accountID]
	if !ok {
		s = &slot{sem: make(chan struct{}, 1)}
		p.byAcct[accountID] = s
	}
	return s
}

func fingerprint(acc *ebsync.Account) string {
	return acc.IMAPAddr + "|" + acc.IMAPUser
}

// Acquire borrows the account's on-demand connection, dialling when needed.
func (p *Pool) Acquire(ctx context.Context, acc *ebsync.Account) (*Lease, error) {
	s := p.slotOf(acc.ID)
	// One connection per account and node: a concurrent request waits a little for it (the admin browser
	// issues list + get together) instead of failing at once; it gives up after acquireWait or at ctx's end.
	wait := time.NewTimer(acquireWait)
	defer wait.Stop()
	select {
	case s.sem <- struct{}{}:
	case <-wait.C:
		if p.Metrics != nil {
			p.Metrics.Busy.Inc()
		}
		return nil, ErrBusy
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	fp := fingerprint(acc)
	if c := s.take(p.idleFor(), fp, p.Metrics); c != nil {
		if p.Metrics != nil {
			p.Metrics.Acquires.WithLabelValues("reuse").Inc()
		}
		return &Lease{Client: c, pool: p, slot: s}, nil
	}
	c, err := p.dial(ctx, acc)
	if err != nil {
		<-s.sem
		return nil, err
	}
	s.mu.Lock()
	s.addr = fp
	s.mu.Unlock()
	if p.Metrics != nil {
		p.Metrics.Acquires.WithLabelValues("dial").Inc()
		p.Metrics.Open.Inc()
	}
	return &Lease{Client: c, pool: p, slot: s}, nil
}

// AcquireErr maps ErrBusy to the RFC 9457 problem used by the API.
func AcquireErr(err error) error {
	if errors.Is(err, ErrBusy) {
		return errs.New(errs.CodeConnectionBusy, "The account's on-demand IMAP connection is busy; retry shortly.")
	}
	return err
}

func (p *Pool) dial(ctx context.Context, acc *ebsync.Account) (*imapc.Client, error) {
	o := imapc.Options{Addr: acc.IMAPAddr, TLS: acc.IMAPTLS, InsecureSkipVerify: acc.IgnoreCertErrors,
		ID: ebsync.MergeIMAPID(p.IMAPID, acc.IMAPID), DisableRev2: acc.DisableRev2}
	if err := ebsync.AuthOptions(ctx, acc, &o); err != nil {
		return nil, err
	}
	if p.Dialer != nil {
		local := acc.LocalAddress
		if p.IPPool != nil {
			local = p.IPPool.ResolveLocal(acc.OrganizationID, acc.ID, "imap", acc.LocalAddress, acc.LocalAddresses, acc.IMAPStrategy)
		}
		o.Dial = p.Dialer.MailDialFunc(egress.MailNetwork{Proxy: acc.Proxy, LocalAddr: local})
	}
	return imapc.Dial(ctx, o)
}

func (s *slot) take(idle time.Duration, fp string, m *Metrics) *imapc.Client {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client == nil {
		return nil
	}
	c := s.client
	s.client = nil
	if s.addr != fp || time.Since(s.since) > idle {
		_ = c.Close()
		if m != nil {
			m.Open.Dec()
		}
		return nil
	}
	return c
}

// Release returns the connection to the pool.
func (l *Lease) Release() {
	if l == nil || l.done {
		return
	}
	l.done = true
	l.slot.mu.Lock()
	if l.slot.client != nil {
		_ = l.slot.client.Close()
		if l.pool.Metrics != nil {
			l.pool.Metrics.Open.Dec()
		}
	}
	l.slot.client = l.Client
	l.slot.since = time.Now()
	l.slot.mu.Unlock()
	<-l.slot.sem
}

// Discard closes the connection instead of pooling it.
func (l *Lease) Discard() {
	if l == nil || l.done {
		return
	}
	l.done = true
	_ = l.Client.Close()
	if l.pool.Metrics != nil {
		l.pool.Metrics.Open.Dec()
	}
	<-l.slot.sem
}

// Sweep closes sessions idle longer than Idle.
func (p *Pool) Sweep() {
	p.mu.Lock()
	slots := make([]*slot, 0, len(p.byAcct))
	for _, s := range p.byAcct {
		slots = append(slots, s)
	}
	p.mu.Unlock()
	cutoff := time.Now().Add(-p.idleFor())
	for _, s := range slots {
		s.mu.Lock()
		if s.client != nil && s.since.Before(cutoff) {
			_ = s.client.Close()
			s.client = nil
			if p.Metrics != nil {
				p.Metrics.IdleCloses.Inc()
				p.Metrics.Open.Dec()
			}
		}
		s.mu.Unlock()
	}
}

// Run sweeps idle sessions until ctx ends, then closes everything.
func (p *Pool) Run(ctx context.Context) {
	every := p.idleFor() / 2
	if every <= 0 || every > 15*time.Second {
		every = 15 * time.Second
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			p.Close()
			return
		case <-t.C:
			p.Sweep()
		}
	}
}

// Close closes every pooled session.
func (p *Pool) Close() {
	p.mu.Lock()
	slots := p.byAcct
	p.byAcct = nil
	p.mu.Unlock()
	for _, s := range slots {
		s.mu.Lock()
		if s.client != nil {
			_ = s.client.Close()
			s.client = nil
			if p.Metrics != nil {
				p.Metrics.Open.Dec()
			}
		}
		s.mu.Unlock()
	}
}

// Metrics holds Prometheus instruments for the pool.
type Metrics struct {
	Acquires   *prometheus.CounterVec
	Busy       prometheus.Counter
	IdleCloses prometheus.Counter
	Open       prometheus.Gauge
}

// NewMetrics builds instruments (caller registers them).
func NewMetrics() *Metrics {
	return &Metrics{
		Acquires: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "eb_ondemand_acquires_total",
			Help: "On-demand IMAP acquires by outcome (reuse|dial).",
		}, []string{"kind"}),
		Busy: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "eb_ondemand_busy_total",
			Help: "On-demand IMAP acquire rejections because the slot was busy.",
		}),
		IdleCloses: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "eb_ondemand_idle_closes_total",
			Help: "On-demand IMAP connections closed after idle timeout.",
		}),
		Open: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "eb_ondemand_open",
			Help: "Currently open on-demand IMAP connections on this node.",
		}),
	}
}

// Collectors returns every collector for registration.
func (m *Metrics) Collectors() []prometheus.Collector {
	if m == nil {
		return nil
	}
	return []prometheus.Collector{m.Acquires, m.Busy, m.IdleCloses, m.Open}
}
