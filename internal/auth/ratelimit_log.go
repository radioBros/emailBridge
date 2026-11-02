package auth

import (
	"context"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
)

// warnRateLimitOnce logs a cache failure at most once per window for the named helper (W35).
func (s *Service) warnRateLimitOnce(ctx context.Context, name string, window time.Duration, err error) {
	if s == nil || s.log == nil {
		return
	}
	if window <= 0 {
		window = time.Minute
	}
	now := time.Now()
	if v, ok := s.rlFailLog.Load(name); ok {
		if last, ok := v.(time.Time); ok && now.Sub(last) < window {
			return
		}
	}
	s.rlFailLog.Store(name, now)
	s.log.WarnContext(ctx, "auth rate limit check failed", "helper", name, "err", err)
}

// cacheAddFailClosed increments a rate-limit counter; cache errors become 503 service_unavailable (W35).
func (s *Service) cacheAddFailClosed(ctx context.Context, key string, window time.Duration, helper string) (int64, error) {
	n, err := s.kv.CacheAdd(ctx, key, 1, window)
	if err != nil {
		s.warnRateLimitOnce(ctx, helper, window, err)
		return 0, errs.New(errs.CodeServiceUnavailable, "Authentication is temporarily unavailable; try again shortly.")
	}
	return n, nil
}
