package submit

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/radiobros/emailbridge/internal/mailproto/smtpc"
)

// ErrPoolBusy is returned when an account already uses every connection it may have. The worker nacks the
// job with a short delay instead of queueing behind the busy account (S4 §4).
var ErrPoolBusy = errors.New("submit: account connection limit reached")

// Pool keeps authenticated SMTP sessions per account: at most PerAccount at a time, each reused for up to
// MaxMessages messages with RSET in between, closed after Idle and after any error (S6 §4).
type Pool struct {
	// Dial opens a new session for an account.
	Dial        func(ctx context.Context, acc *SMTPAccount) (*smtpc.Session, error)
	MaxMessages int
	Idle        time.Duration
	PerAccount  int

	mu       sync.Mutex
	accounts map[string]*accountPool
}

type accountPool struct {
	slots chan struct{}
	mu    sync.Mutex
	idle  []parked
}

type parked struct {
	session *smtpc.Session
	since   time.Time
}

// Lease is one borrowed session. Release returns it to the pool (or closes it when it is spent or dead).
type Lease struct {
	Session *smtpc.Session
	pool    *Pool
	account *accountPool
	done    bool
}

func (p *Pool) perAccount() int {
	if p.PerAccount > 0 {
		return p.PerAccount
	}
	return 2
}

func (p *Pool) maxMessages() int {
	if p.MaxMessages > 0 {
		return p.MaxMessages
	}
	return 100
}

func (p *Pool) idleFor() time.Duration {
	if p.Idle > 0 {
		return p.Idle
	}
	return 10 * time.Minute
}

func (p *Pool) poolOf(accountID string) *accountPool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.accounts == nil {
		p.accounts = map[string]*accountPool{}
	}
	ap, ok := p.accounts[accountID]
	if !ok {
		ap = &accountPool{slots: make(chan struct{}, p.perAccount())}
		p.accounts[accountID] = ap
	}
	return ap
}

// Acquire takes a free slot of the account and returns a usable session, reusing an idle one when it is
// still alive. It does not wait: a fully busy account yields ErrPoolBusy.
func (p *Pool) Acquire(ctx context.Context, acc *SMTPAccount) (*Lease, error) {
	ap := p.poolOf(acc.PoolKey())
	select {
	case ap.slots <- struct{}{}:
	default:
		return nil, ErrPoolBusy
	}
	if s := ap.take(p.idleFor()); s != nil {
		if err := s.Reset(ctx); err == nil {
			return &Lease{Session: s, pool: p, account: ap}, nil
		}
		_ = s.Close()
	}
	s, err := p.Dial(ctx, acc)
	if err != nil {
		<-ap.slots
		return nil, err
	}
	return &Lease{Session: s, pool: p, account: ap}, nil
}

// take pops a live idle session, closing the ones that sat for longer than idle.
func (ap *accountPool) take(idle time.Duration) *smtpc.Session {
	cutoff := time.Now().Add(-idle)
	ap.mu.Lock()
	defer ap.mu.Unlock()
	for len(ap.idle) > 0 {
		last := ap.idle[len(ap.idle)-1]
		ap.idle = ap.idle[:len(ap.idle)-1]
		if last.since.Before(cutoff) || last.session.Dead() {
			_ = last.session.Close()
			continue
		}
		return last.session
	}
	return nil
}

// Release gives the slot back. The session is kept only when it is alive and has messages left.
func (l *Lease) Release() {
	if l == nil || l.done {
		return
	}
	l.done = true
	keep := !l.Session.Dead() && l.Session.Messages() < l.pool.maxMessages()
	if keep {
		l.account.mu.Lock()
		l.account.idle = append(l.account.idle, parked{session: l.Session, since: time.Now()})
		l.account.mu.Unlock()
	} else {
		_ = l.Session.Close()
	}
	<-l.account.slots
}

// Discard closes the session instead of pooling it (after any error, S6 §4).
func (l *Lease) Discard() {
	if l == nil || l.done {
		return
	}
	l.done = true
	_ = l.Session.Close()
	<-l.account.slots
}

// Sweep closes sessions that have been idle for longer than Idle.
func (p *Pool) Sweep() {
	p.mu.Lock()
	pools := make([]*accountPool, 0, len(p.accounts))
	for _, ap := range p.accounts {
		pools = append(pools, ap)
	}
	p.mu.Unlock()
	cutoff := time.Now().Add(-p.idleFor())
	for _, ap := range pools {
		ap.mu.Lock()
		live := ap.idle[:0]
		for _, s := range ap.idle {
			if s.since.Before(cutoff) || s.session.Dead() {
				_ = s.session.Close()
				continue
			}
			live = append(live, s)
		}
		ap.idle = live
		ap.mu.Unlock()
	}
}

// Run sweeps idle sessions until ctx ends, then closes everything.
func (p *Pool) Run(ctx context.Context) {
	every := p.idleFor() / 2
	if every <= 0 || every > time.Minute {
		every = time.Minute
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
	pools := p.accounts
	p.accounts = nil
	p.mu.Unlock()
	for _, ap := range pools {
		ap.mu.Lock()
		for _, s := range ap.idle {
			_ = s.session.Close()
		}
		ap.idle = nil
		ap.mu.Unlock()
	}
}
