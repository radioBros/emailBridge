package protolog

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/logging"
)

type capture struct {
	mu   sync.Mutex
	recs []slog.Record
	ctxs []context.Context
}

func (c *capture) Enabled(context.Context, slog.Level) bool { return true }
func (c *capture) Handle(ctx context.Context, r slog.Record) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.recs = append(c.recs, r)
	c.ctxs = append(c.ctxs, ctx)
	return nil
}
func (c *capture) WithAttrs([]slog.Attr) slog.Handler { return c }
func (c *capture) WithGroup(string) slog.Handler      { return c }

func attr(r slog.Record, key string) string {
	v := ""
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == key {
			v = a.Value.String()
			return false
		}
		return true
	})
	return v
}

func TestRecorderCapsPerAccountAndDay(t *testing.T) {
	c := &capture{}
	now := time.Date(2026, 10, 26, 23, 59, 0, 0, time.UTC)
	r := &Recorder{Log: slog.New(c), Cap: 10, Now: func() time.Time { return now }}
	s := r.Sink("org_1", "acc_1", false)
	other := r.Sink("org_1", "acc_2", false)
	s(Line{Dir: 'C', Proto: "imap", ConnID: "acc_1/main", Text: "a1 NOOP", Bytes: 9}) // 7 bytes
	s(Line{Dir: 'S', Proto: "imap", ConnID: "acc_1/main", Text: "a1 OK", Bytes: 7})   // 12 > 10: marker
	s(Line{Dir: 'C', Proto: "imap", ConnID: "acc_1/main", Text: "x", Bytes: 3})       // dropped
	other(Line{Dir: 'C', Proto: "imap", ConnID: "acc_2/main", Text: "a1 NOOP", Bytes: 9})
	if len(c.recs) != 3 {
		t.Fatalf("records %d", len(c.recs))
	}
	if c.recs[0].Message != "protocol" || attr(c.recs[0], "dir") != "C" || attr(c.recs[0], "line") != "a1 NOOP" || attr(c.recs[0], "connId") != "acc_1/main" {
		t.Fatalf("record %+v", c.recs[0])
	}
	if ids := logging.IDsFrom(c.ctxs[0]); ids.AccountID != "acc_1" || ids.OrganizationID != "org_1" {
		t.Fatalf("ids %+v", ids)
	}
	if !strings.Contains(c.recs[1].Message, "cap reached") || c.recs[1].Level != slog.LevelWarn {
		t.Fatalf("marker %+v", c.recs[1])
	}
	// next UTC day: the cap resets
	now = now.Add(2 * time.Minute)
	s(Line{Dir: 'C', Proto: "imap", Text: "a2 NOOP"})
	if len(c.recs) != 4 {
		t.Fatal("cap must reset at UTC midnight")
	}
	r.Forget("acc_1")
	if r.caps(true) != DefaultRawCap || (&Recorder{}).caps(false) != DefaultAccountCap || (&Recorder{}).now().IsZero() {
		t.Fatal("defaults")
	}
}
