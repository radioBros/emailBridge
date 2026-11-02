package cluster

import (
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
)

func tp(t time.Time) *time.Time { return &t }

// resolveWindow: presets, the custom window and every validation rule of from / to.
func TestResolveWindowCustom(t *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)

	if p, w, err := resolveWindow(KPIRequest{}, now); err != nil || p != "24h" || w.n != 24 || !w.to.Equal(now) {
		t.Fatalf("default: %s %+v %v", p, w, err)
	}
	if _, _, err := resolveWindow(KPIRequest{Period: "2h"}, now); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("unknown period: %v", err)
	}

	from := now.Add(-10 * 24 * time.Hour)
	p, w, err := resolveWindow(KPIRequest{From: tp(from), To: tp(now)}, now)
	if err != nil || p != PeriodCustom {
		t.Fatalf("custom: %s %v", p, err)
	}
	if w.spec.bucket != 12*time.Hour || w.n != 20 || !w.prevStart.Equal(from.Add(-10*24*time.Hour)) {
		t.Fatalf("10 days: bucket %s n %d prevStart %s", w.spec.bucket, w.n, w.prevStart)
	}
	if p, _, err := resolveWindow(KPIRequest{Period: PeriodCustom, From: tp(from), To: tp(now)}, now); err != nil || p != PeriodCustom {
		t.Fatalf("period=custom with from/to: %s %v", p, err)
	}

	// A `to` in the future counts up to now.
	if _, w, err := resolveWindow(KPIRequest{From: tp(now.Add(-time.Hour)), To: tp(now.Add(48 * time.Hour))}, now); err != nil || !w.to.Equal(now) {
		t.Fatalf("future to: %+v %v", w, err)
	}

	bad := []KPIRequest{
		{Period: "7d", From: tp(from), To: tp(now)}, // both styles
		{From: tp(from)}, // to missing
		{To: tp(now)},    // from missing
		{From: tp(now), To: tp(now.Add(-time.Hour))},                   // reversed
		{From: tp(now.Add(time.Hour)), To: tp(now.Add(2 * time.Hour))}, // entirely in the future
		{From: tp(now.Add(-4 * time.Minute)), To: tp(now)},             // shorter than 5 minutes
		{From: tp(now.Add(-MaxCustomRange - time.Hour)), To: tp(now)},  // longer than 92 days
	}
	for i, r := range bad {
		if _, _, err := resolveWindow(r, now); errs.CodeOf(err) != errs.CodeValidationFailed {
			t.Fatalf("case %d: want validation_failed, got %v", i, err)
		}
	}
	if _, w, err := resolveWindow(KPIRequest{From: tp(now.Add(-MaxCustomRange)), To: tp(now)}, now); err != nil || w.n > maxCustomBuckets {
		t.Fatalf("92 days must be accepted with at most %d buckets: n %d %v", maxCustomBuckets, w.n, err)
	}
}

// Bucket ladder: the finest width that keeps the sparkline at no more than 31 points.
func TestCustomWindowBuckets(t *testing.T) {
	base := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		length time.Duration
		bucket time.Duration
		n      int
	}{
		{2 * time.Hour, 5 * time.Minute, 24},
		{61 * time.Minute, 5 * time.Minute, 13},
		{24 * time.Hour, time.Hour, 24},
		{3 * 24 * time.Hour, 3 * time.Hour, 24},
		{31 * 24 * time.Hour, 24 * time.Hour, 31},
		{92 * 24 * time.Hour, 72 * time.Hour, 31},
	}
	for _, c := range cases {
		w := newCustomWindow(base, base.Add(c.length))
		if w.spec.bucket != c.bucket || w.n != c.n {
			t.Fatalf("%s: bucket %s n %d, want %s %d", c.length, w.spec.bucket, w.n, c.bucket, c.n)
		}
	}
}

// A length that is not a multiple of the bucket: each period is bucketed from its own start and the last
// bucket is clamped, so the current period always starts at index n.
func TestCustomWindowIndexSplit(t *testing.T) {
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	w := newCustomWindow(from, from.Add(61*time.Minute)) // 13 buckets of 5 min, the last one 1 min
	cases := []struct {
		at   time.Time
		want int
	}{
		{w.prevStart, 0},
		{from.Add(-time.Second), w.n - 1},
		{from, w.n},
		{from.Add(7 * time.Minute), w.n + 1},
		{w.to.Add(-time.Second), 2*w.n - 1},
		{w.to, -1},
		{w.prevStart.Add(-time.Second), -1},
	}
	for _, c := range cases {
		if got := w.index(c.at); got != c.want {
			t.Fatalf("index(%s) = %d, want %d", c.at, got, c.want)
		}
	}
}
