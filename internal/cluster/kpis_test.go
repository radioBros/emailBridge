package cluster

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
)

func TestPeriodsTable(t *testing.T) {
	want := map[string]int{"1h": 12, "24h": 24, "7d": 28, "30d": 30}
	for p, n := range want {
		if got := periods[p].buckets(); got != n {
			t.Fatalf("%s: %d buckets, want %d", p, got, n)
		}
	}
	if _, ok := periods[DefaultPeriod]; !ok {
		t.Fatal("default period missing")
	}
}

func TestWindowIndexAndSeries(t *testing.T) {
	now := time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)
	w := newWindow(periods["24h"], now)
	if !w.from.Equal(now.Add(-24*time.Hour)) || !w.prevStart.Equal(now.Add(-48*time.Hour)) {
		t.Fatalf("window: %+v", w)
	}
	cases := []struct {
		t   time.Time
		idx int
	}{
		{now.Add(-49 * time.Hour), -1},
		{now.Add(-48 * time.Hour), 0},
		{now.Add(-24 * time.Hour), 24},
		{now.Add(-time.Minute), 47},
		{now, -1},
	}
	for _, c := range cases {
		if got := w.index(c.t); got != c.idx {
			t.Fatalf("index(%v)=%d want %d", c.t, got, c.idx)
		}
	}
	s := newSeries(24)
	s.add(0, 2)
	s.add(23, 3)
	s.add(24, 7)
	s.add(47, 1)
	s.add(-1, 100)
	s.add(48, 100)
	k := s.kpi(w, time.Time{})
	if k.Current != 8 || k.Previous == nil || *k.Previous != 5 || len(k.Buckets) != 24 || k.Buckets[0] != 7 || k.Partial {
		t.Fatalf("kpi: %+v", k)
	}
	// previous period older than the cutoff: no trend
	k = s.kpi(w, now.Add(-30*time.Hour))
	if k.Previous != nil || k.Partial {
		t.Fatalf("cutoff inside previous: %+v", k)
	}
	// current period older than the cutoff: partial
	k = s.kpi(w, now.Add(-12*time.Hour))
	if k.Previous != nil || !k.Partial {
		t.Fatalf("cutoff inside current: %+v", k)
	}
	if got := seriesOr(map[string]*series{}, "x", 3); len(got.raw) != 6 {
		t.Fatal("seriesOr")
	}
}

func TestRetentionDefaults(t *testing.T) {
	r := Retention{Events: time.Hour}.withDefaults()
	d := DefaultRetention()
	if r.Events != time.Hour || r.Deliveries != d.Deliveries || r.Outbound != d.Outbound {
		t.Fatalf("%+v", r)
	}
	s := &Service{}
	if s.retention() != d {
		t.Fatal("nil retention")
	}
	s.Retention = func() Retention { return Retention{} }
	if s.retention() != d {
		t.Fatal("zero retention")
	}
}

func TestLatencyBinsAndP95(t *testing.T) {
	if latencyBin(0) != 0 || latencyBin(10*time.Millisecond) != 0 || latencyBin(11*time.Millisecond) != 1 ||
		latencyBin(time.Minute) != len(latencyBoundsMs) {
		t.Fatal("bins")
	}
	var bins [latencyBins]int64
	if p95FromBins(bins) != nil {
		t.Fatal("empty p95")
	}
	bins[0] = 94
	bins[4] = 6 // 6 % ≤ 250 ms
	if p := p95FromBins(bins); p == nil || *p != 250 {
		t.Fatalf("p95: %v", p)
	}
	bins = [latencyBins]int64{}
	bins[0] = 96
	bins[4] = 4
	if p := p95FromBins(bins); *p != 10 {
		t.Fatalf("p95 low: %d", *p)
	}
	bins = [latencyBins]int64{}
	bins[latencyBins-1] = 1
	if p := p95FromBins(bins); *p != 10000 {
		t.Fatalf("p95 overflow: %d", *p)
	}
}

func TestCountable(t *testing.T) {
	for p, want := range map[string]bool{
		"/v1/accounts": true, "/v1/stats": false, "/healthz": false, "/readyz": false, "/assets/a.js": false,
		"/v1/admin/ws": false, "/v1/events/stream": false, "/v1/logs/export": false, "/v1/exports/x/download": false,
		"/v1/stats/other": true,
	} {
		if countable(p) != want {
			t.Fatalf("%s: %v", p, !want)
		}
	}
}

func TestCounterRecordAndMiddleware(t *testing.T) {
	c := &APICounter{now: func() time.Time { return time.Unix(0, 0) }}
	if !c.clock().Equal(time.Unix(0, 0).UTC()) {
		t.Fatal("clock")
	}
	c.Record("", 200, 0, time.Now()) // ignored
	at := time.Date(2026, 10, 27, 10, 5, 30, 0, time.UTC)
	c.Record("org_a", 200, 5*time.Millisecond, at)
	c.Record("org_a", 404, 30*time.Millisecond, at)
	c.Record("org_a", 503, 2*time.Second, at.Add(10*time.Second))
	c.Record("org_b", 200, time.Millisecond, at)
	batch := c.take()
	if len(batch) != 2 {
		t.Fatalf("keys: %d", len(batch))
	}
	a := batch[apiCallKey{org: "org_a", minute: at.Truncate(time.Minute).Unix()}]
	if a.success != 1 || a.clientError != 1 || a.serverError != 1 || a.lat[0] != 1 || a.lat[2] != 1 || a.lat[7] != 1 {
		t.Fatalf("agg: %+v", a)
	}
	c.putBack(batch)
	c.putBack(map[apiCallKey]*apiCallAgg{{org: "org_b", minute: at.Truncate(time.Minute).Unix()}: {success: 2}})
	if got := c.take()[apiCallKey{org: "org_b", minute: at.Truncate(time.Minute).Unix()}].success; got != 3 {
		t.Fatalf("merge: %d", got)
	}
	if c.Flush(context.Background()) != nil {
		t.Fatal("empty flush must be a no-op")
	}

	h := c.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/fail":
			w.WriteHeader(http.StatusInternalServerError)
			w.WriteHeader(http.StatusOK) // second call ignored for counting
		case "/v1/zero":
			w.WriteHeader(0)
		default:
			_, _ = w.Write([]byte("ok"))
		}
	}))
	do := func(path string, org string) {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		if org != "" {
			r = r.WithContext(store.WithOrganization(r.Context(), org))
		}
		h.ServeHTTP(httptest.NewRecorder(), r)
	}
	do("/v1/accounts", "org_m")
	do("/v1/fail", "org_m")
	do("/v1/zero", "org_m")
	do("/v1/stats", "org_m")    // excluded
	do("/v1/accounts", "")      // no organisation
	do("/v1/admin/ws", "org_m") // long-lived
	var ok, se int64
	for k, a := range c.take() {
		if k.org != "org_m" {
			t.Fatalf("unexpected org %q", k.org)
		}
		ok += a.success
		se += a.serverError
	}
	if ok != 2 || se != 1 {
		t.Fatalf("middleware counts ok=%d 5xx=%d", ok, se)
	}

	sw := &apiStatusWriter{ResponseWriter: httptest.NewRecorder()}
	if sw.Unwrap() == nil {
		t.Fatal("unwrap")
	}
	if apiCallDocID("org_x", at.Truncate(time.Hour)) != "org_x|2026102710" {
		t.Fatal(apiCallDocID("org_x", at))
	}
}

func TestKPIsRequiresOrgAndValidPeriod(t *testing.T) {
	s := &Service{}
	if _, err := s.KPIs(context.Background(), KPIRequest{Period: "24h"}); errs.CodeOf(err) != errs.CodeForbidden {
		t.Fatalf("no org: %v", err)
	}
	ctx := store.WithOrganization(context.Background(), "org_x")
	if _, err := s.KPIs(ctx, KPIRequest{Period: "2h"}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("bad period: %v", err)
	}
	if kpiCacheKey("org_x", "1h", window{}) != "stats:v1:org_x:1h" {
		t.Fatal("cache key")
	}
	pctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	<-pctx.Done()
	if probeError(pctx) != "timeout" || probeError(context.Background()) != "unreachable" {
		t.Fatal("probeError")
	}
}
