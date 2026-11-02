package archive

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/ippool"
	"github.com/radiobros/emailbridge/internal/mailproto/imapc"
	ebsync "github.com/radiobros/emailbridge/internal/sync"
)

// ErrPoolBusy is returned when the account's single on-demand slot is already in use.
var ErrPoolBusy = errors.New("archive: on-demand IMAP connection busy")

const defaultIdle = 60 * time.Second

// Pool keeps at most one on-demand IMAP connection per account (S5 §3), idle-closed after Idle.
type Pool struct {
	Dialer *egress.Dialer
	IPPool *ippool.Selector
	IMAPID map[string]string
	Idle   time.Duration

	mu     sync.Mutex
	byAcct map[string]*slot
}

type slot struct {
	sem    chan struct{}
	mu     sync.Mutex
	client *imapc.Client
	since  time.Time
	addr   string // dial fingerprint: addr|user
}

// Lease is one borrowed on-demand IMAP connection.
type Lease struct {
	Client *imapc.Client
	pool   *Pool
	slot   *slot
	done   bool
}

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
	select {
	case s.sem <- struct{}{}:
	default:
		return nil, ErrPoolBusy
	}
	fp := fingerprint(acc)
	if c := s.take(p.idleFor(), fp); c != nil {
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
	return &Lease{Client: c, pool: p, slot: s}, nil
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

// take pops a live idle client matching fp, closing stale or mismatched ones.
func (s *slot) take(idle time.Duration, fp string) *imapc.Client {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client == nil {
		return nil
	}
	c := s.client
	s.client = nil
	if s.addr != fp || time.Since(s.since) > idle {
		_ = c.Close()
		return nil
	}
	return c
}

// Release returns the connection to the pool (or closes it after Discard).
func (l *Lease) Release() {
	if l == nil || l.done {
		return
	}
	l.done = true
	l.slot.mu.Lock()
	if l.slot.client != nil {
		_ = l.slot.client.Close()
	}
	l.slot.client = l.Client
	l.slot.since = time.Now()
	l.slot.mu.Unlock()
	<-l.slot.sem
}

// Discard closes the connection instead of pooling it (after errors).
func (l *Lease) Discard() {
	if l == nil || l.done {
		return
	}
	l.done = true
	_ = l.Client.Close()
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
		}
		s.mu.Unlock()
	}
}
