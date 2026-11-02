package autoconfig

import (
	"context"
	"net"
	"net/http"
	"net/mail"
	"strings"
	"sync"
	"time"

	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// HTTPDoer is the subset of http.Client used for discovery fetches.
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Resolver looks up DNS records (injectable in tests).
type Resolver interface {
	LookupSRV(ctx context.Context, service, proto, name string) (string, []*net.SRV, error)
	LookupMX(ctx context.Context, name string) ([]*net.MX, error)
}

type netResolver struct{}

func (netResolver) LookupSRV(ctx context.Context, service, proto, name string) (string, []*net.SRV, error) {
	return net.DefaultResolver.LookupSRV(ctx, service, proto, name)
}

func (netResolver) LookupMX(ctx context.Context, name string) ([]*net.MX, error) {
	return net.DefaultResolver.LookupMX(ctx, name)
}

// Service discovers mail settings.
type Service struct {
	HTTP   HTTPDoer
	DNS    Resolver
	Cache  *resultCache
	clock  func() time.Time
	rateMu sync.Mutex
	rate   map[string]*tokenBucket
}

type tokenBucket struct {
	tokens float64
	last   time.Time
}

const ratePerMin = 30.0

// NewService builds a Service. When dialer is non-nil it supplies the HTTP client.
func NewService(dialer *egress.Dialer) *Service {
	s := &Service{
		DNS:   netResolver{},
		Cache: newCache(cacheTTL),
		clock: time.Now,
		rate:  map[string]*tokenBucket{},
	}
	if dialer == nil {
		dialer = egress.NewDialer(egress.Policy{})
	}
	s.HTTP = dialer.PublicOnly().HTTPClient(httpTO)
	return s
}

// Discover runs the full discovery pipeline for email.
func (s *Service) Discover(ctx context.Context, email string) (*Result, error) {
	addr, domain, err := parseEmail(email)
	if err != nil {
		return nil, err
	}
	if cached, ok := s.Cache.get(domain); ok {
		out := cached
		out.Email = addr
		return &out, nil
	}
	out := &Result{Email: addr, Domain: domain, Source: SourceNone}

	if r := lookupBuiltin(domain); r != nil {
		r.Email = addr
		s.Cache.put(domain, *r)
		return r, nil
	}

	if r := s.tryISPDB(ctx, domain); r != nil && r.complete() {
		r.Email = addr
		r.Domain = domain
		r.Source = SourceISPDB
		s.Cache.put(domain, *r)
		return r, nil
	}

	if r := s.tryAutodiscover(ctx, addr, domain); r != nil && r.complete() {
		r.Email = addr
		r.Domain = domain
		r.Source = SourceAutodiscover
		s.Cache.put(domain, *r)
		return r, nil
	}

	if r := s.trySRV(ctx, domain); r != nil {
		r.Email = addr
		r.Domain = domain
		if r.complete() {
			r.Source = SourceSRV
			s.Cache.put(domain, *r)
			return r, nil
		}
		out = merge(out, r)
	}

	if r := s.tryMX(ctx, domain); r != nil && r.complete() {
		r.Email = addr
		r.Domain = domain
		s.Cache.put(domain, *r)
		return r, nil
	}

	if out.IMAP != nil || out.SMTP != nil {
		out.Source = SourceSRV
		s.Cache.put(domain, *out)
		return out, nil
	}
	s.Cache.put(domain, *out)
	return out, nil
}

// DiscoverForOrg is the authenticated API entry (tenancy + rate limit).
func (s *Service) DiscoverForOrg(ctx context.Context, email string) (*Result, error) {
	if err := tenancy.RequireRead(ctx, "accounts"); err != nil {
		return nil, err
	}
	if org, ok := store.OrganizationFrom(ctx); ok {
		if err := s.takeToken(ctx, org); err != nil {
			return nil, err
		}
	}
	return s.Discover(ctx, email)
}

func (s *Service) takeToken(ctx context.Context, org string) error {
	s.rateMu.Lock()
	defer s.rateMu.Unlock()
	now := s.clock()
	b := s.rate[org]
	if b == nil {
		b = &tokenBucket{tokens: ratePerMin, last: now}
		s.rate[org] = b
	}
	elapsed := now.Sub(b.last).Minutes()
	b.tokens += elapsed * ratePerMin
	if b.tokens > ratePerMin {
		b.tokens = ratePerMin
	}
	b.last = now
	if b.tokens < 1 {
		info := errs.TokenBucketInfo(ratePerMin, ratePerMin, b.tokens)
		httpapi.NoteRateLimit(ctx, info)
		return errs.New(errs.CodeRateLimited, "Autoconfig is limited to 30 requests per minute per organisation.").WithRate(info)
	}
	b.tokens--
	httpapi.NoteRateLimit(ctx, errs.TokenBucketInfo(ratePerMin, ratePerMin, b.tokens))
	return nil
}

func parseEmail(raw string) (addr, domain string, err error) {
	raw = strings.TrimSpace(raw)
	a, e := mail.ParseAddress(raw)
	if e != nil || a == nil || a.Address == "" {
		return "", "", errs.New(errs.CodeValidationFailed, "A valid email address is required.").WithSafe("email", raw)
	}
	addr = strings.ToLower(a.Address)
	at := strings.LastIndexByte(addr, '@')
	if at <= 0 || at == len(addr)-1 {
		return "", "", errs.New(errs.CodeValidationFailed, "A valid email address is required.").WithSafe("email", raw)
	}
	return addr, addr[at+1:], nil
}

func merge(base, extra *Result) *Result {
	if base == nil {
		return extra
	}
	if extra == nil {
		return base
	}
	out := *base
	if out.IMAP == nil && extra.IMAP != nil {
		out.IMAP = extra.IMAP
	}
	if out.SMTP == nil && extra.SMTP != nil {
		out.SMTP = extra.SMTP
	}
	if out.OAuthHint == "" {
		out.OAuthHint = extra.OAuthHint
	}
	return &out
}

func (s *Service) tryMX(ctx context.Context, domain string) *Result {
	cctx, cancel := context.WithTimeout(ctx, dnsTO)
	defer cancel()
	mx, err := s.DNS.LookupMX(cctx, domain)
	if err != nil || len(mx) == 0 {
		return nil
	}
	return lookupFromMX(mx[0].Host)
}
