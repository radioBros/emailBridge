package protolog

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/radiobros/emailbridge/internal/logging"
)

// Default per-account daily caps (S12 §7: protocolLogs.accountCap, raw debug 50 MiB).
const (
	DefaultAccountCap = 10 << 20
	DefaultRawCap     = 50 << 20
)

// Recorder turns tapped lines into `protocol`-category log records (one per line: dir, proto, connId,
// line, bytes) with the account and organisation IDs attached, and enforces the per-account daily cap: the
// first line over the cap writes one marker record, later lines are dropped until the next UTC day.
type Recorder struct {
	Log    *slog.Logger // logging.CatProtocol logger
	Cap    int64        // normal mode, per account per UTC day (default 10 MiB)
	RawCap int64        // raw debug mode (default 50 MiB)
	Now    func() time.Time

	mu   sync.Mutex
	used map[string]*usage
}

type usage struct {
	day    string
	bytes  int64
	capped bool
}

func (r *Recorder) caps(raw bool) int64 {
	if raw {
		if r.RawCap > 0 {
			return r.RawCap
		}
		return DefaultRawCap
	}
	if r.Cap > 0 {
		return r.Cap
	}
	return DefaultAccountCap
}

func (r *Recorder) now() time.Time {
	if r.Now != nil {
		return r.Now().UTC()
	}
	return time.Now().UTC()
}

// allow books n bytes for the account; it returns (write, marker): marker is true exactly once per day
// when the cap is first exceeded.
func (r *Recorder) allow(accountID string, n int64, raw bool) (write, marker bool) {
	day := r.now().Format(time.DateOnly)
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.used == nil {
		r.used = map[string]*usage{}
	}
	u := r.used[accountID]
	if u == nil || u.day != day {
		u = &usage{day: day}
		r.used[accountID] = u
	}
	if u.capped {
		return false, false
	}
	if u.bytes+n > r.caps(raw) {
		u.capped = true
		return false, true
	}
	u.bytes += n
	return true, false
}

// Sink returns the tap sink of one account connection. raw selects the raw-debug cap (the tap's Mode
// decides what content is captured; credentials are masked in every mode).
func (r *Recorder) Sink(organizationID, accountID string, raw bool) Sink {
	ctx := logging.With(context.Background(), func(ids *logging.IDs) {
		ids.OrganizationID, ids.AccountID = organizationID, accountID
	})
	return func(l Line) {
		write, marker := r.allow(accountID, int64(len(l.Text)), raw)
		if marker {
			r.Log.LogAttrs(ctx, slog.LevelWarn, "protocol log cap reached; further lines are dropped until the next UTC day",
				slog.Int64("cap", r.caps(raw)), slog.Bool("raw", raw))
		}
		if !write {
			return
		}
		r.Log.LogAttrs(ctx, slog.LevelDebug, "protocol",
			slog.String("dir", string(l.Dir)), slog.String("proto", l.Proto), slog.String("connId", l.ConnID),
			slog.String("line", l.Text), slog.Int("bytes", l.Bytes))
	}
}

// Forget drops the usage counters of an account (erasure, tests).
func (r *Recorder) Forget(accountID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.used, accountID)
}
