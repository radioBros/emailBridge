package store

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/logging"
)

// TestPerformanceSmoke inserts records in log-system-sized batches (1,000) and runs filtered queries;
// it logs the numbers and fails only on gross regressions. It inserts 10k records by default and 100k with
// EB_LOG_PERF=1. Skipped with -short.
func TestPerformanceSmoke(t *testing.T) {
	if testing.Short() {
		t.Skip("performance smoke")
	}
	s := openTest(t, nil)
	ctx := context.Background()
	total := 10_000
	if os.Getenv("EB_LOG_PERF") == "1" {
		total = 100_000
	}
	const batchSize = 1000
	start := time.Now()
	for b := 0; b < total/batchSize; b++ {
		batch := make([]logging.Record, 0, batchSize)
		for i := 0; i < batchSize; i++ {
			n := b*batchSize + i
			ts := base.Add(-time.Duration(n) * 100 * time.Millisecond) // ~2.8 h, one day file per category
			batch = append(batch, rec(ts, n, []logging.Category{logging.CatAPI, logging.CatSync, logging.CatDelivery}[n%3], logging.LevelInfo,
				"request handled", func(r *logging.Record) {
					r.IDs.OrganizationID = fmt.Sprintf("org_%d", n%10)
					r.IDs.AccountID = fmt.Sprintf("acc_%d", n%500)
					r.IDs.RequestID = fmt.Sprintf("req_%d", n)
					r.Attrs = map[string]any{"route": "/v1/accounts/{accountId}", "status": 200 + n%5, "latencyMs": n % 97}
					if n%1000 == 0 {
						r.Msg = "upstream connection refused"
					}
				}))
		}
		if err := s.Write(ctx, batch); err != nil {
			t.Fatal(err)
		}
	}
	insert := time.Since(start)
	f := Filter{From: base.Add(-24 * time.Hour), To: base.Add(time.Hour), Limit: 100}
	measure := func(name string, mut func(*Filter)) time.Duration {
		q := f
		mut(&q)
		start := time.Now()
		got, err := s.Search(ctx, q)
		if err != nil || len(got) == 0 {
			t.Fatalf("%s: %d %v", name, len(got), err)
		}
		d := time.Since(start)
		t.Logf("query %-28s %4d records in %v", name, len(got), d)
		return d
	}
	t.Logf("inserted %d records in %v (%.0f records/s)", total, insert, float64(total)/insert.Seconds())
	worst := time.Duration(0)
	for name, mut := range map[string]func(*Filter){
		"newest 100":                 func(*Filter) {},
		"account + category":         func(q *Filter) { q.AccountID = "acc_42"; q.Categories = []logging.Category{logging.CatAPI} },
		"organisation + level info+": func(q *Filter) { q.OrgID = "org_3"; q.Level = "info" },
		"request id":                 func(q *Filter) { q.RequestID = "req_7777" },
		"full text":                  func(q *Filter) { q.Q = "connection refused" },
		"full text attrs":            func(q *Filter) { q.Q = "accounts"; q.OrgID = "org_1" },
	} {
		worst = max(worst, measure(name, mut))
	}
	stStart := time.Now()
	if _, err := s.Stats(ctx, f); err != nil {
		t.Fatal(err)
	}
	t.Logf("stats over all records in %v", time.Since(stStart))
	// generous bounds: the numbers are logged; only a gross regression fails (shared CI machines vary)
	if rate := float64(total) / insert.Seconds(); rate < 100 || worst > 10*time.Second {
		t.Fatalf("too slow: %.0f records/s, worst query %v", rate, worst)
	}
}

// TestPerformanceWideRange measures a query that must open every day file of a 31-day range across all
// 15 categories (465 files) and finds nothing, plus Stats over the same range. EB_LOG_PERF=1 only.
func TestPerformanceWideRange(t *testing.T) {
	if testing.Short() || os.Getenv("EB_LOG_PERF") != "1" {
		t.Skip("set EB_LOG_PERF=1")
	}
	s := openTest(t, func(o *Options) { o.MaxOpen = 8 })
	ctx := context.Background()
	start := time.Now()
	for d := 0; d < 31; d++ {
		var batch []logging.Record
		for ci, c := range logging.Categories {
			for i := 0; i < 20; i++ {
				ts := base.Add(-time.Duration(d)*24*time.Hour - time.Duration(i)*time.Minute)
				batch = append(batch, rec(ts, d*10000+ci*100+i, c, logging.LevelInfo, "routine record", nil))
			}
		}
		if err := s.Write(ctx, batch); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("seeded 465 day files in %v", time.Since(start))
	f := Filter{From: base.Add(-31 * 24 * time.Hour), To: base.Add(time.Hour), Limit: 100, RequestID: "req_absent"}
	s.CloseIdle()
	s.mu.Lock()
	for k, w := range s.writers { // cold start: no open handles
		_ = w.close()
		delete(s.writers, k)
	}
	s.mu.Unlock()
	for _, pass := range []string{"cold", "warm"} {
		start = time.Now()
		got, err := s.Search(ctx, f)
		if err != nil || len(got) != 0 {
			t.Fatalf("needle: %d %v", len(got), err)
		}
		t.Logf("needle query over 31 days x 15 categories (%s) in %v", pass, time.Since(start))
	}
	f.RequestID, f.Q = "", "absent words"
	start = time.Now()
	if _, err := s.Search(ctx, f); err != nil {
		t.Fatal(err)
	}
	t.Logf("full-text miss over 31 days x 15 categories in %v", time.Since(start))
	start = time.Now()
	st, err := s.Stats(ctx, Filter{From: f.From, To: f.To})
	if err != nil || st.ByLevel["info"] != 31*15*20 {
		t.Fatalf("stats %+v %v", st, err)
	}
	t.Logf("stats over 31 days x 15 categories in %v", time.Since(start))
}
