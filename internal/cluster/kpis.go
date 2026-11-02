package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// Retention is the data retention of the KPI sources (EB_RETENTION_*); a previous period older than the
// cutoff has no trend (W74 §1.2).
type Retention struct {
	Events     time.Duration
	Deliveries time.Duration
	Outbound   time.Duration
}

// DefaultRetention matches the S9 defaults.
func DefaultRetention() Retention {
	return Retention{Events: 7 * 24 * time.Hour, Deliveries: 30 * 24 * time.Hour, Outbound: 90 * 24 * time.Hour}
}

func (r Retention) withDefaults() Retention {
	d := DefaultRetention()
	if r.Events <= 0 {
		r.Events = d.Events
	}
	if r.Deliveries <= 0 {
		r.Deliveries = d.Deliveries
	}
	if r.Outbound <= 0 {
		r.Outbound = d.Outbound
	}
	return r
}

// kpiCacheTTL is how long the organisation-scoped KPIs stay in eb-cache.
const kpiCacheTTL = 15 * time.Second

// periodSpec is one row of the W74 §1.1 table.
type periodSpec struct {
	length time.Duration
	bucket time.Duration
}

func (p periodSpec) buckets() int { return int(p.length / p.bucket) }

var periods = map[string]periodSpec{
	"1h":  {length: time.Hour, bucket: 5 * time.Minute},
	"24h": {length: 24 * time.Hour, bucket: time.Hour},
	"7d":  {length: 7 * 24 * time.Hour, bucket: 6 * time.Hour},
	"30d": {length: 30 * 24 * time.Hour, bucket: 24 * time.Hour},
}

// DefaultPeriod is used when ?period is absent.
const DefaultPeriod = "24h"

// PeriodCustom is the period reported for an explicit from / to window.
const PeriodCustom = "custom"

// Custom window limits: at most one quarter (the sparkline then has 31 three-day buckets at most) and at
// least five minutes (one bucket of the finest width).
const (
	MaxCustomRange = 92 * 24 * time.Hour
	MinCustomRange = 5 * time.Minute
)

// customBuckets is the bucket ladder of custom windows: the finest width that keeps a sparkline at no
// more than maxCustomBuckets points.
var customBuckets = []time.Duration{
	5 * time.Minute, 15 * time.Minute, 30 * time.Minute, time.Hour, 2 * time.Hour, 3 * time.Hour,
	6 * time.Hour, 12 * time.Hour, 24 * time.Hour, 48 * time.Hour, 72 * time.Hour,
}

const maxCustomBuckets = 31

// KPIRequest selects the KPI window: a preset Period, or an explicit From / To (period "custom").
type KPIRequest struct {
	Period string
	From   *time.Time
	To     *time.Time
}

// KPI is one counter for the period with its previous-period value and sparkline buckets.
type KPI struct {
	Current int64 `json:"current" doc:"Count in the period." example:"852"`
	// Previous is null when the previous period is older than the source's retention.
	Previous *int64  `json:"previous" doc:"Count in the previous equal period; null when older than the source's retention." example:"741"`
	Buckets  []int64 `json:"buckets" doc:"Sparkline buckets of the current period (12, 24, 28 or 30 entries for the presets; at most 31 for a custom window, the last one possibly shorter)." example:"[31,28,40,35,52,61,70,66,58,49,44,38]"`
	// Partial is true when the current period starts before the retention cutoff.
	Partial bool `json:"partial,omitempty" doc:"The current period starts before the retention cutoff." example:"false"`
}

// AccountKPIs are point-in-time account counts.
type AccountKPIs struct {
	Total     int64 `json:"total" doc:"All accounts." example:"47"`
	Connected int64 `json:"connected" doc:"Accounts in state connected." example:"43"`
	Attention int64 `json:"attention" doc:"authError + connectError + disabled." example:"3"`
}

// APICallKPIs are the organisation's API calls in the period (needs logs.read).
type APICallKPIs struct {
	Succeeded    KPI    `json:"succeeded" doc:"API calls answered with 2xx or 3xx."`
	Failed       KPI    `json:"failed" doc:"API calls answered with 4xx or 5xx."`
	ClientErrors int64  `json:"clientErrors" doc:"4xx responses in the period." example:"61"`
	ServerErrors int64  `json:"serverErrors" doc:"5xx responses in the period." example:"4"`
	LatencyP95Ms *int64 `json:"latencyP95Ms" doc:"Upper bound of the latency bin holding the 95th percentile (10000 = 10 s or more); null without calls." example:"250"`
}

// QueueKPI is one JetStream work queue (instance owner).
type QueueKPI struct {
	Key        string `json:"key" enum:"webhooks,submission,sysmail,events" doc:"Work queue: webhooks, submission, sysmail or events." example:"webhooks"`
	Stream     string `json:"stream" doc:"JetStream stream name." example:"EMAILBRIDGE_DELIVERY"`
	Waiting    uint64 `json:"waiting" doc:"Messages waiting." example:"128"`
	InProgress int    `json:"inProgress" doc:"Handed to a worker, not yet acknowledged." example:"6"`
	Paused     bool   `json:"paused" doc:"Processing paused cluster-wide." example:"false"`
}

// InfraKPIs are latencies measured by the stats call (instance owner).
type InfraKPIs struct {
	MongoPingMs float64 `json:"mongoPingMs" doc:"MongoDB ping measured by this call." example:"1.4"`
	MongoError  string  `json:"mongoError,omitempty" enum:"timeout,unreachable" doc:"Set when the MongoDB ping failed: timeout or unreachable." example:"timeout"`
	BusRTTMs    float64 `json:"busRttMs" doc:"Message-bus round trip measured by this call." example:"0.3"`
	BusError    string  `json:"busError,omitempty" enum:"timeout,unreachable" doc:"Set when the message-bus round trip failed: timeout or unreachable." example:"unreachable"`
}

// KPIs is the `kpis` object of GET /v1/stats (W74 §1.2).
type KPIs struct {
	Period            string       `json:"period" enum:"1h,24h,7d,30d,custom" doc:"The period of these KPIs; custom for an explicit from / to window." example:"24h"`
	From              time.Time    `json:"from" doc:"Start of the current period (UTC)." example:"2026-10-26T12:00:00Z"`
	To                time.Time    `json:"to" doc:"End of the current period (UTC)." example:"2026-10-27T12:00:00Z"`
	BucketSeconds     int64        `json:"bucketSeconds" doc:"Width of one sparkline bucket." example:"3600"`
	GeneratedAt       time.Time    `json:"generatedAt" doc:"When the cached organisation part was computed." example:"2026-10-27T11:59:40Z"`
	Accounts          AccountKPIs  `json:"accounts" doc:"Point-in-time account counts."`
	MessagesReceived  KPI          `json:"messagesReceived" doc:"Messages received (indexed) in the period."`
	MessagesSent      KPI          `json:"messagesSent" doc:"Outbound messages sent in the period (by sent time)."`
	MessagesFailed    KPI          `json:"messagesFailed" doc:"Outbound messages that failed, created in the period."`
	WebhooksDelivered KPI          `json:"webhooksDelivered" doc:"Webhook deliveries that succeeded, created in the period."`
	WebhooksFailed    KPI          `json:"webhooksFailed" doc:"Webhook deliveries that failed or were dead-lettered, created in the period."`
	APICalls          *APICallKPIs `json:"apiCalls,omitempty" doc:"Needs logs.read."`
	Queues            []QueueKPI   `json:"queues,omitempty" doc:"Instance owner only."`
	Infra             *InfraKPIs   `json:"infra,omitempty" doc:"Instance owner only."`
}

// window is the resolved time range of one stats call: the current period [from, to) and the previous
// period of equal length [prevStart, from), each split into n buckets.
type window struct {
	spec      periodSpec
	n         int
	to        time.Time
	from      time.Time
	prevStart time.Time
}

func newWindow(spec periodSpec, now time.Time) window {
	to := now.UTC().Truncate(time.Second)
	from := to.Add(-spec.length)
	return window{spec: spec, n: spec.buckets(), to: to, from: from, prevStart: from.Add(-spec.length)}
}

// newCustomWindow is the window of an explicit range; the bucket width comes from customBuckets and the
// last bucket of each period may be shorter (the length need not be a multiple of the width).
func newCustomWindow(from, to time.Time) window {
	length := to.Sub(from)
	bucket := customBuckets[len(customBuckets)-1]
	for _, b := range customBuckets {
		if ceilDiv(length, b) <= maxCustomBuckets {
			bucket = b
			break
		}
	}
	return window{
		spec: periodSpec{length: length, bucket: bucket}, n: ceilDiv(length, bucket),
		to: to, from: from, prevStart: from.Add(-length),
	}
}

func ceilDiv(a, b time.Duration) int { return int((a + b - 1) / b) }

// index maps t to a bucket index over both periods: [0, n) previous, [n, 2n) current; -1 outside.
// Each period is bucketed from its own start, so both line up even when the length is not a multiple of
// the bucket width.
func (w window) index(t time.Time) int {
	if t.Before(w.prevStart) || !t.Before(w.to) {
		return -1
	}
	if t.Before(w.from) {
		return min(int(t.Sub(w.prevStart)/w.spec.bucket), w.n-1)
	}
	return w.n + min(int(t.Sub(w.from)/w.spec.bucket), w.n-1)
}

// series accumulates bucket counts over both periods.
type series struct {
	n   int
	raw []int64
}

func newSeries(n int) *series { return &series{n: n, raw: make([]int64, 2*n)} }

func (s *series) add(idx int, v int64) {
	if idx >= 0 && idx < len(s.raw) {
		s.raw[idx] += v
	}
}

// kpi builds the KPI; cutoff is the oldest retained time (zero: no retention limit).
func (s *series) kpi(w window, cutoff time.Time) KPI {
	k := KPI{Buckets: append([]int64(nil), s.raw[s.n:]...)}
	var prev int64
	for i, v := range s.raw {
		if i < s.n {
			prev += v
		} else {
			k.Current += v
		}
	}
	if cutoff.IsZero() || !w.prevStart.Before(cutoff) {
		k.Previous = &prev
	}
	if !cutoff.IsZero() && w.from.Before(cutoff) {
		k.Partial = true
	}
	return k
}

// bucketExpr is the aggregation expression of the bucket index of field over both periods (same rule as
// window.index: each period bucketed from its own start, the last bucket clamped).
func bucketExpr(w window, field string) bson.M {
	in := func(start time.Time, base int) bson.M {
		return bson.M{"$add": bson.A{base, bson.M{"$min": bson.A{
			bson.M{"$floor": bson.M{"$divide": bson.A{
				bson.M{"$subtract": bson.A{"$" + field, start}},
				w.spec.bucket.Milliseconds(),
			}}},
			w.n - 1,
		}}}}
	}
	return bson.M{"$cond": bson.A{
		bson.M{"$lt": bson.A{"$" + field, w.from}},
		in(w.prevStart, 0),
		in(w.from, w.n),
	}}
}

// bucketCounts runs one $match + $group by (key, bucket) and returns per-key series.
func (s *Service) bucketCounts(ctx context.Context, w window, coll, timeField string, match bson.M, keyExpr any) (map[string]*series, error) {
	match[timeField] = bson.M{"$gte": w.prevStart, "$lt": w.to}
	cur, err := s.DB.Collection(coll).Aggregate(ctx, bson.A{
		bson.M{"$match": match},
		bson.M{"$group": bson.M{
			"_id": bson.M{"k": keyExpr, "b": bucketExpr(w, timeField)},
			"n":   bson.M{"$sum": 1},
		}},
	})
	if err != nil {
		return nil, errs.New(errs.CodeInternalError, "Could not aggregate KPIs.").WithSafe("collection", coll).WithSafe("error", err.Error())
	}
	defer func() { _ = cur.Close(ctx) }()
	out := map[string]*series{}
	for cur.Next(ctx) {
		var row struct {
			ID struct {
				K string  `bson:"k"`
				B float64 `bson:"b"`
			} `bson:"_id"`
			N int64 `bson:"n"`
		}
		if err := cur.Decode(&row); err != nil {
			return nil, errs.New(errs.CodeInternalError, "Could not decode KPIs.").WithSafe("collection", coll)
		}
		sr := out[row.ID.K]
		if sr == nil {
			sr = newSeries(w.n)
			out[row.ID.K] = sr
		}
		sr.add(int(row.ID.B), row.N)
	}
	if err := cur.Err(); err != nil {
		return nil, errs.New(errs.CodeInternalError, "Could not read KPIs.").WithSafe("collection", coll).WithSafe("error", err.Error())
	}
	return out, nil
}

func seriesOr(m map[string]*series, key string, n int) *series {
	if s := m[key]; s != nil {
		return s
	}
	return newSeries(n)
}

func (s *Service) retention() Retention {
	if s.Retention == nil {
		return DefaultRetention()
	}
	return s.Retention().withDefaults()
}

// orgKPIs computes the organisation-scoped part (cacheable): accounts, the five KPIs and API calls.
// The retention cut-offs are measured from now (not from the window's end), so a past custom window that
// reaches beyond what is kept is reported as partial / without a previous value.
func (s *Service) orgKPIs(ctx context.Context, org, period string, w window, now time.Time) (*KPIs, error) {
	n := w.n
	ret := s.retention()
	now = now.UTC().Truncate(time.Second)
	out := &KPIs{
		Period: period, From: w.from, To: w.to, BucketSeconds: int64(w.spec.bucket / time.Second), GeneratedAt: now,
	}

	accounts, err := countByState(ctx, s.DB, "accounts", bson.M{"organizationId": org})
	if err != nil {
		return nil, err
	}
	for st, c := range accounts {
		out.Accounts.Total += c
		switch st {
		case "connected":
			out.Accounts.Connected += c
		case "authError", "connectError", "disabled":
			out.Accounts.Attention += c
		}
	}

	evs, err := s.bucketCounts(ctx, w, "events", "time",
		bson.M{"organizationId": org, "type": "message.received"}, "$type")
	if err != nil {
		return nil, err
	}
	out.MessagesReceived = seriesOr(evs, "message.received", n).kpi(w, now.Add(-ret.Events))

	sent, err := s.bucketCounts(ctx, w, "outboundMessages", "sentAt",
		bson.M{"organizationId": org, "state": "sent"}, "$state")
	if err != nil {
		return nil, err
	}
	out.MessagesSent = seriesOr(sent, "sent", n).kpi(w, now.Add(-ret.Outbound))

	failed, err := s.bucketCounts(ctx, w, "outboundMessages", "createdAt",
		bson.M{"organizationId": org, "state": "failed"}, "$state")
	if err != nil {
		return nil, err
	}
	out.MessagesFailed = seriesOr(failed, "failed", n).kpi(w, now.Add(-ret.Outbound))

	dlv, err := s.bucketCounts(ctx, w, "deliveries", "createdAt",
		bson.M{"organizationId": org, "state": bson.M{"$in": bson.A{"succeeded", "failed", "deadLettered"}}},
		bson.M{"$cond": bson.A{bson.M{"$eq": bson.A{"$state", "succeeded"}}, "ok", "failed"}})
	if err != nil {
		return nil, err
	}
	dcut := now.Add(-ret.Deliveries)
	out.WebhooksDelivered = seriesOr(dlv, "ok", n).kpi(w, dcut)
	out.WebhooksFailed = seriesOr(dlv, "failed", n).kpi(w, dcut)

	api, err := s.apiCallKPIs(ctx, org, w)
	if err != nil {
		return nil, err
	}
	out.APICalls = api
	return out, nil
}

// apiCallKPIs reads the hourly API-call buckets of both periods (minute resolution for 1h / 24h).
func (s *Service) apiCallKPIs(ctx context.Context, org string, w window) (*APICallKPIs, error) {
	n := w.n
	useMinutes := w.spec.length <= 24*time.Hour
	opts := options.Find()
	if !useMinutes {
		opts.SetProjection(bson.M{"min": 0})
	}
	cur, err := s.DB.Collection(APICallsCollection).Find(ctx, bson.M{
		"organizationId": org,
		"hour":           bson.M{"$gte": w.prevStart.Truncate(time.Hour), "$lt": w.to},
	}, opts)
	if err != nil {
		return nil, errs.New(errs.CodeInternalError, "Could not read API call counters.").WithSafe("error", err.Error())
	}
	defer func() { _ = cur.Close(ctx) }()
	ok, bad := newSeries(n), newSeries(n)
	var bins [latencyBins]int64
	out := &APICallKPIs{}
	curHour := w.from.Truncate(time.Hour)
	for cur.Next(ctx) {
		var d apiCallDoc
		if err := cur.Decode(&d); err != nil {
			return nil, errs.New(errs.CodeInternalError, "Could not decode API call counters.")
		}
		if useMinutes {
			for key, m := range d.Min {
				var mm int
				if _, err := fmt.Sscanf(key, "m%d", &mm); err != nil {
					continue
				}
				t := d.Hour.Add(time.Duration(mm) * time.Minute)
				idx := w.index(t)
				ok.add(idx, m.S)
				bad.add(idx, m.C+m.E)
				if idx >= n {
					out.ClientErrors += m.C
					out.ServerErrors += m.E
				}
			}
		} else {
			idx := w.index(d.Hour)
			if d.Hour.Before(w.prevStart) {
				idx = 0
			}
			ok.add(idx, d.Success)
			bad.add(idx, d.ClientError+d.ServerError)
			if idx >= n {
				out.ClientErrors += d.ClientError
				out.ServerErrors += d.ServerError
			}
		}
		if !d.Hour.Before(curHour) {
			for i := range bins {
				bins[i] += d.Lat[fmt.Sprintf("b%d", i)]
			}
		}
	}
	if err := cur.Err(); err != nil {
		return nil, errs.New(errs.CodeInternalError, "Could not read API call counters.").WithSafe("error", err.Error())
	}
	out.Succeeded = ok.kpi(w, time.Time{})
	out.Failed = bad.kpi(w, time.Time{})
	out.LatencyP95Ms = p95FromBins(bins)
	return out, nil
}

// kpiCacheKey keys presets by name and custom windows by their exact bounds.
func kpiCacheKey(org, period string, w window) string {
	if period == PeriodCustom {
		return fmt.Sprintf("stats:v1:%s:custom:%d-%d", org, w.from.Unix(), w.to.Unix())
	}
	return "stats:v1:" + org + ":" + period
}

// cachedOrgKPIs returns the organisation part from eb-cache or computes and stores it.
func (s *Service) cachedOrgKPIs(ctx context.Context, org, period string, w window, now time.Time) (*KPIs, error) {
	key := kpiCacheKey(org, period, w)
	if s.Bus != nil {
		if e, err := s.Bus.CacheGet(ctx, key); err == nil {
			var k KPIs
			if jerr := json.Unmarshal(e.Value, &k); jerr == nil {
				return &k, nil
			}
		} else if !errors.Is(err, bus.ErrCacheMiss) && s.Log != nil {
			s.Log.Debug("stats cache read failed", "err", err)
		}
	}
	k, err := s.orgKPIs(ctx, org, period, w, now)
	if err != nil {
		return nil, err
	}
	if s.Bus != nil {
		if raw, jerr := json.Marshal(k); jerr == nil {
			if cerr := s.Bus.CacheSet(ctx, key, raw, kpiCacheTTL); cerr != nil && s.Log != nil {
				s.Log.Debug("stats cache write failed", "err", cerr)
			}
		}
	}
	return k, nil
}

// queueKeys maps work streams to the dashboard queue keys.
var queueKeys = []struct{ key, stream string }{
	{"webhooks", bus.StreamDelivery},
	{"submission", bus.StreamSubmit},
	{"sysmail", bus.StreamSysmail},
	{"events", bus.StreamEvents},
}

func (s *Service) queueKPIs(ctx context.Context) []QueueKPI {
	out := make([]QueueKPI, 0, len(queueKeys))
	for _, q := range queueKeys {
		st, err := s.Bus.QueueStats(ctx, q.stream)
		if err != nil {
			if s.Log != nil {
				s.Log.Warn("queue stats unavailable for dashboard", "queue", q.stream, "err", err)
			}
			continue
		}
		out = append(out, QueueKPI{Key: q.key, Stream: q.stream, Waiting: st.Pending, InProgress: st.AckPending, Paused: st.Paused})
	}
	return out
}

func probeError(ctx context.Context) string {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "timeout"
	}
	return "unreachable"
}

func (s *Service) infraKPIs(ctx context.Context) *InfraKPIs {
	out := &InfraKPIs{}
	pctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	start := time.Now()
	if err := s.DB.Ping(pctx); err != nil {
		out.MongoError = probeError(pctx)
	} else {
		out.MongoPingMs = float64(time.Since(start).Microseconds()) / 1000
	}
	cancel()
	if s.Bus == nil || s.Bus.Conn == nil {
		out.BusError = "unreachable"
		return out
	}
	rtt, err := s.Bus.Conn.RTT()
	if err != nil {
		out.BusError = "unreachable"
		if errors.Is(err, context.DeadlineExceeded) {
			out.BusError = "timeout"
		}
		return out
	}
	out.BusRTTMs = float64(rtt.Microseconds()) / 1000
	return out
}

// resolveWindow validates the request and returns its period name and window.
func resolveWindow(req KPIRequest, now time.Time) (string, window, error) {
	now = now.UTC().Truncate(time.Second)
	if req.From == nil && req.To == nil {
		period := req.Period
		if period == "" {
			period = DefaultPeriod
		}
		spec, ok := periods[period]
		if !ok {
			return "", window{}, errs.New(errs.CodeValidationFailed, "period must be one of 1h, 24h, 7d, 30d, or send from and to.").
				WithSafe("period", period)
		}
		return period, newWindow(spec, now), nil
	}
	if req.Period != "" && req.Period != PeriodCustom {
		return "", window{}, errs.New(errs.CodeValidationFailed, "Send either period or from and to, not both.")
	}
	if req.From == nil || req.To == nil {
		return "", window{}, errs.New(errs.CodeValidationFailed, "A custom window needs both from and to.")
	}
	from := req.From.UTC().Truncate(time.Second)
	to := req.To.UTC().Truncate(time.Second)
	if to.After(now) {
		to = now // a window ending in the future counts up to now
	}
	if !from.Before(to) {
		return "", window{}, errs.New(errs.CodeValidationFailed, "from must be before to (and before now).")
	}
	if to.Sub(from) < MinCustomRange {
		return "", window{}, errs.New(errs.CodeValidationFailed, "The window must be at least 5 minutes long.")
	}
	if to.Sub(from) > MaxCustomRange {
		return "", window{}, errs.New(errs.CodeValidationFailed, "The window can be at most 92 days long.").
			WithSafe("maxDays", int(MaxCustomRange/(24*time.Hour)))
	}
	return PeriodCustom, newCustomWindow(from, to), nil
}

// KPIs returns the W74 dashboard counters for a preset period or a custom window, gated by the caller's
// permissions (§1.4).
func (s *Service) KPIs(ctx context.Context, req KPIRequest) (*KPIs, error) {
	org, ok := store.OrganizationFrom(ctx)
	if !ok || org == "" {
		return nil, errs.New(errs.CodeForbidden, "An organisation context is required.")
	}
	now := time.Now()
	period, w, err := resolveWindow(req, now)
	if err != nil {
		return nil, err
	}
	k, err := s.cachedOrgKPIs(ctx, org, period, w, now)
	if err != nil {
		return nil, err
	}
	if tenancy.Require(ctx, tenancy.PermLogsRead) != nil {
		k.APICalls = nil
	}
	k.Queues, k.Infra = nil, nil
	if tenancy.Require(ctx, tenancy.PermClusterRead) == nil {
		if s.Bus != nil {
			k.Queues = s.queueKPIs(ctx)
		}
		k.Infra = s.infraKPIs(ctx)
	}
	return k, nil
}
