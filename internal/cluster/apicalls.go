package cluster

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/store"
)

// APICallsCollection holds the per-organisation hourly API-call buckets (W74 §2).
const APICallsCollection = "apiCallStats"

// apiCallsRetention is how long hourly API-call buckets are kept (TTL on expireAt).
const apiCallsRetention = 90 * 24 * time.Hour

// latencyBoundsMs are the upper bounds of the latency bins; the last bin (index len) is "> 10 s".
var latencyBoundsMs = [...]int64{10, 25, 50, 100, 250, 500, 1000, 2500, 5000, 10000}

const latencyBins = len(latencyBoundsMs) + 1

type apiCallKey struct {
	org    string
	minute int64 // Unix seconds of the UTC minute
}

type apiCallAgg struct {
	success, clientError, serverError int64
	lat                               [latencyBins]int64
}

func (a *apiCallAgg) merge(b *apiCallAgg) {
	a.success += b.success
	a.clientError += b.clientError
	a.serverError += b.serverError
	for i := range a.lat {
		a.lat[i] += b.lat[i]
	}
}

// APICounter counts API calls per organisation in memory and flushes them every minute into hourly
// MongoDB buckets (W74 §2). It never writes per request.
type APICounter struct {
	DB  *store.DB
	Log *slog.Logger

	now     func() time.Time
	mu      sync.Mutex
	pending map[apiCallKey]*apiCallAgg
}

func (c *APICounter) clock() time.Time {
	if c.now != nil {
		return c.now().UTC()
	}
	return time.Now().UTC()
}

func latencyBin(d time.Duration) int {
	ms := d.Milliseconds()
	for i, b := range latencyBoundsMs {
		if ms <= b {
			return i
		}
	}
	return len(latencyBoundsMs)
}

// Record adds one call of org with HTTP status and latency at time at.
func (c *APICounter) Record(org string, status int, latency time.Duration, at time.Time) {
	if org == "" {
		return
	}
	k := apiCallKey{org: org, minute: at.UTC().Truncate(time.Minute).Unix()}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pending == nil {
		c.pending = map[apiCallKey]*apiCallAgg{}
	}
	a := c.pending[k]
	if a == nil {
		a = &apiCallAgg{}
		c.pending[k] = a
	}
	switch {
	case status >= 500:
		a.serverError++
	case status >= 400:
		a.clientError++
	default:
		a.success++
	}
	a.lat[latencyBin(latency)]++
}

// countable reports whether a request path is counted (W74 §2: not the stats endpoint itself, not health
// checks, not the SPA, not long-lived streams).
func countable(p string) bool {
	if !strings.HasPrefix(p, "/v1/") {
		return false
	}
	if p == "/v1/stats" || p == "/v1/admin/ws" {
		return false
	}
	return !strings.HasSuffix(p, "/stream") && !strings.HasSuffix(p, "/export") && !strings.HasSuffix(p, "/download")
}

type apiStatusWriter struct {
	http.ResponseWriter
	status int
}

func (w *apiStatusWriter) WriteHeader(code int) {
	if code == 0 { // huma's zero Status field means the operation's default 200 (as httpapi.statusWriter)
		code = http.StatusOK
	}
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *apiStatusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (w *apiStatusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Middleware counts every countable /v1 request that carries an organisation. Registered with
// httpapi.Server.AddMiddleware after tenancy, so the caller's organisation is in the request context.
func (c *APICounter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		org, ok := store.OrganizationFrom(r.Context())
		if !ok || !countable(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		sw := &apiStatusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r)
		status := sw.status
		if status <= 0 {
			status = http.StatusOK
		}
		c.Record(org, status, time.Since(start), start)
	})
}

func (c *APICounter) take() map[apiCallKey]*apiCallAgg {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.pending
	c.pending = nil
	return out
}

func (c *APICounter) putBack(batch map[apiCallKey]*apiCallAgg) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pending == nil {
		c.pending = map[apiCallKey]*apiCallAgg{}
	}
	for k, a := range batch {
		if cur := c.pending[k]; cur != nil {
			cur.merge(a)
		} else {
			c.pending[k] = a
		}
	}
}

func apiCallDocID(org string, hour time.Time) string {
	return org + "|" + hour.Format("2006010215")
}

// Flush writes the pending counts as one BulkWrite of $inc upserts; on failure the counts are kept for
// the next flush.
func (c *APICounter) Flush(ctx context.Context) error {
	batch := c.take()
	if len(batch) == 0 {
		return nil
	}
	models := make([]mongo.WriteModel, 0, len(batch))
	for k, a := range batch {
		minute := time.Unix(k.minute, 0).UTC()
		hour := minute.Truncate(time.Hour)
		mm := fmt.Sprintf("min.m%02d.", minute.Minute())
		inc := bson.M{
			"success": a.success, "clientError": a.clientError, "serverError": a.serverError,
			mm + "s": a.success, mm + "c": a.clientError, mm + "e": a.serverError,
		}
		for i, n := range a.lat {
			if n != 0 {
				inc[fmt.Sprintf("lat.b%d", i)] = n
			}
		}
		models = append(models, mongo.NewUpdateOneModel().
			SetFilter(bson.M{"_id": apiCallDocID(k.org, hour)}).
			SetUpdate(bson.M{
				"$inc": inc,
				"$setOnInsert": bson.M{
					"organizationId": k.org, "hour": hour, "expireAt": hour.Add(apiCallsRetention),
				},
			}).
			SetUpsert(true))
	}
	if _, err := c.DB.Collection(APICallsCollection).BulkWrite(ctx, models, options.BulkWrite().SetOrdered(false)); err != nil {
		c.putBack(batch)
		return err
	}
	return nil
}

// Run flushes every interval until ctx ends, then flushes once more with a short deadline.
func (c *APICounter) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			fctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			c.flushLogged(fctx)
			cancel()
			return
		case <-t.C:
			c.flushLogged(ctx)
		}
	}
}

func (c *APICounter) flushLogged(ctx context.Context) {
	if err := c.Flush(ctx); err != nil && c.Log != nil {
		c.Log.Warn("api call counters flush failed; kept for the next flush", "err", err)
	}
}

// apiCallDoc is one stored hourly bucket.
type apiCallDoc struct {
	Hour        time.Time                   `bson:"hour"`
	Success     int64                       `bson:"success"`
	ClientError int64                       `bson:"clientError"`
	ServerError int64                       `bson:"serverError"`
	Lat         map[string]int64            `bson:"lat"`
	Min         map[string]apiCallMinuteDoc `bson:"min"`
}

type apiCallMinuteDoc struct {
	S int64 `bson:"s"`
	C int64 `bson:"c"`
	E int64 `bson:"e"`
}

// p95FromBins returns the upper bound of the bin holding the 95th percentile, or nil without calls.
func p95FromBins(bins [latencyBins]int64) *int64 {
	var total int64
	for _, n := range bins {
		total += n
	}
	if total == 0 {
		return nil
	}
	threshold := (total*95 + 99) / 100 // ceil(0.95·total)
	var cum int64
	for i, n := range bins {
		cum += n
		if cum >= threshold {
			v := latencyBoundsMs[min(i, len(latencyBoundsMs)-1)]
			return &v
		}
	}
	v := latencyBoundsMs[len(latencyBoundsMs)-1]
	return &v
}
