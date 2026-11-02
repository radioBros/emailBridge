package throttle

import (
	"context"
	"fmt"
	"time"
)

// minuteKey formats the UTC minute bucket id.
func minuteKey(t time.Time) string {
	return t.UTC().Format("200601021504")
}

func domainKey(org, domain string, t time.Time) string {
	return fmt.Sprintf("eb:throttle:d:%s:%s:%s", org, domain, minuteKey(t))
}

func providerKey(org, family string, t time.Time) string {
	return fmt.Sprintf("eb:throttle:p:%s:%s:%s", org, family, minuteKey(t))
}

// untilNextMinute returns how long until the next UTC minute (+ tiny skew).
func untilNextMinute(now time.Time) time.Duration {
	next := now.UTC().Truncate(time.Minute).Add(time.Minute)
	d := next.Sub(now)
	if d < time.Second {
		return time.Second
	}
	return d
}

// TakeResult is the outcome of trying to take tokens for a message.
type TakeResult struct {
	OK    bool
	Delay time.Duration // when !OK
}

// Buckets checks and consumes per-domain / per-provider tokens.
type Buckets struct {
	Counter Counter
	Limits  Limits
	Now     func() time.Time
}

func (b *Buckets) now() time.Time {
	if b.Now != nil {
		return b.Now().UTC()
	}
	return time.Now().UTC()
}

// Check reports whether the message may proceed without consuming tokens.
func (b *Buckets) Check(ctx context.Context, org string, domains []string) (TakeResult, error) {
	return b.take(ctx, org, domains, false)
}

// Take consumes one token per distinct domain and provider when under budget.
func (b *Buckets) Take(ctx context.Context, org string, domains []string) (TakeResult, error) {
	return b.take(ctx, org, domains, true)
}

func (b *Buckets) take(ctx context.Context, org string, domains []string, consume bool) (TakeResult, error) {
	if b == nil || b.Counter == nil || org == "" || len(domains) == 0 {
		return TakeResult{OK: true}, nil
	}
	lim := b.Limits
	if lim.DomainPerMinute == 0 {
		lim = DefaultLimits()
	}
	now := b.now()
	ttl := 2 * time.Minute
	retry := lim.RetryDelay()
	if u := untilNextMinute(now); u > retry {
		retry = u
	}

	type need struct {
		key   string
		limit int
	}
	var needs []need
	seenP := map[string]struct{}{}
	for _, d := range domains {
		needs = append(needs, need{key: domainKey(org, d, now), limit: lim.DomainLimit(d)})
		if fam := ProviderOf(d); fam != "" {
			if _, ok := seenP[fam]; ok {
				continue
			}
			seenP[fam] = struct{}{}
			if pl := lim.ProviderLimit(fam); pl > 0 {
				needs = append(needs, need{key: providerKey(org, fam, now), limit: pl})
			}
		}
	}

	for _, n := range needs {
		cur, err := b.Counter.Get(ctx, n.key)
		if err != nil {
			return TakeResult{}, err
		}
		if cur >= int64(n.limit) {
			return TakeResult{OK: false, Delay: retry}, nil
		}
	}
	if !consume {
		return TakeResult{OK: true}, nil
	}
	for _, n := range needs {
		v, err := b.Counter.Add(ctx, n.key, 1, ttl)
		if err != nil {
			return TakeResult{}, err
		}
		if v > int64(n.limit) {
			// slight overshoot: delay this message; leave the count (approximate)
			return TakeResult{OK: false, Delay: retry}, nil
		}
	}
	return TakeResult{OK: true}, nil
}
