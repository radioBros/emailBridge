//go:build integration

package cluster

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// A past custom window through GET /v1/stats?from=&to=: counts inside the window, the previous window of
// equal length, sparkline size, the "custom" period, and the 422 answers for bad windows.
func TestIntegrationKPIsCustomWindow(t *testing.T) {
	h := newHarness(t)
	org := store.NewID(store.PrefixOrganization)
	now := time.Now().UTC().Truncate(time.Second)
	day := 24 * time.Hour
	from, to := now.Add(-14*day), now.Add(-7*day) // 7 days → 6 h buckets, 28 points

	n := 0
	ev := func(at time.Time) bson.M {
		n++
		return bson.M{"_id": store.NewID(store.PrefixEvent), "organizationId": org, "type": "message.received", "time": at}
	}
	h.insert("events",
		ev(from.Add(time.Hour)), ev(from.Add(3*day)), ev(to.Add(-time.Minute)), // current: 3
		ev(from.Add(-2*day)),                          // previous: 1
		ev(to.Add(time.Minute)), ev(from.Add(-8*day)), // outside both
	)

	admin := tenancy.Caller{Kind: tenancy.CallerUser, UserID: "usr_a", OrganizationID: org, Role: tenancy.RoleAdmin}
	q := url.Values{"from": {from.Format(time.RFC3339)}, "to": {to.Format(time.RFC3339)}}
	w := h.do(http.MethodGet, "/v1/stats?"+q.Encode(), nil, admin)
	if w.Code != 200 {
		t.Fatalf("custom stats: %d %s", w.Code, w.Body.String())
	}
	var v StatsView
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	k := v.KPIs
	if k == nil || k.Period != PeriodCustom || !k.From.Equal(from) || !k.To.Equal(to) || k.BucketSeconds != int64(6*time.Hour/time.Second) {
		t.Fatalf("window: %+v", k)
	}
	r := k.MessagesReceived
	if r.Current != 3 || r.Previous == nil || *r.Previous != 1 || len(r.Buckets) != 28 || sum(r.Buckets) != 3 {
		t.Fatalf("messagesReceived: %+v", r)
	}

	for name, query := range map[string]url.Values{
		"too long":   {"from": {now.Add(-100 * day).Format(time.RFC3339)}, "to": {now.Format(time.RFC3339)}},
		"reversed":   {"from": {to.Format(time.RFC3339)}, "to": {from.Format(time.RFC3339)}},
		"half":       {"from": {from.Format(time.RFC3339)}},
		"not a time": {"from": {"yesterday"}, "to": {to.Format(time.RFC3339)}},
		"mixed":      {"period": {"7d"}, "from": {from.Format(time.RFC3339)}, "to": {to.Format(time.RFC3339)}},
	} {
		if w := h.do(http.MethodGet, "/v1/stats?"+query.Encode(), nil, admin); w.Code != 422 {
			t.Fatalf("%s: want 422, got %d %s", name, w.Code, w.Body.String())
		}
	}
}
