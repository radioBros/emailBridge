//go:build integration

package submit

import (
	"context"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/errs"
)

// TestIntegrationListCreatedRange: GET /v1/outbound-messages takes the date range in createdFrom / createdTo
// (RFC 3339, to exclusive) while from / to stay envelope-address filters (W70 Outbox bug); bad dates and an
// empty window are 422.
func TestIntegrationListCreatedRange(t *testing.T) {
	h := newHarness(t)
	base := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	ids := make([]string, 3)
	for i := range ids {
		ids[i] = h.submit(SubmitRequest{})
		if _, err := h.db.Collection(Collection).UpdateOne(context.Background(), bson.M{"_id": ids[i]},
			bson.M{"$set": bson.M{"createdAt": base.Add(time.Duration(i) * 24 * time.Hour)}}); err != nil {
			t.Fatal(err)
		}
	}
	m := &Module{service: h.svc}
	list := func(in listInput) []string {
		t.Helper()
		out, err := m.list(h.ctx, &in)
		if err != nil {
			t.Fatalf("%+v: %v", in, err)
		}
		got := make([]string, 0, len(out.Body.Data))
		for _, v := range out.Body.Data {
			got = append(got, v.ID)
		}
		return got
	}
	day := func(i int) string { return base.Add(time.Duration(i) * 24 * time.Hour).Format(time.RFC3339) }

	if got := list(listInput{CreatedFrom: day(1)}); len(got) != 2 || got[0] != ids[2] || got[1] != ids[1] {
		t.Fatalf("from day 1: %v", got)
	}
	if got := list(listInput{CreatedTo: day(1)}); len(got) != 1 || got[0] != ids[0] {
		t.Fatalf("to day 1 (exclusive): %v", got)
	}
	if got := list(listInput{CreatedFrom: day(1), CreatedTo: day(2)}); len(got) != 1 || got[0] != ids[1] {
		t.Fatalf("day 1 only: %v", got)
	}
	// Offset timezones are accepted (the dashboard passes the KPI window as given).
	if got := list(listInput{CreatedFrom: base.Add(24 * time.Hour).In(time.FixedZone("CET", 3600)).Format(time.RFC3339Nano)}); len(got) != 2 {
		t.Fatalf("offset zone: %v", got)
	}
	// from / to remain envelope-address filters, combinable with the range.
	if got := list(listInput{From: testUser, CreatedFrom: day(2)}); len(got) != 1 || got[0] != ids[2] {
		t.Fatalf("from address + range: %v", got)
	}
	if got := list(listInput{To: testTo}); len(got) != 3 {
		t.Fatalf("to address: %v", got)
	}
	if got := list(listInput{To: "nobody@example.org"}); len(got) != 0 {
		t.Fatalf("unknown to address: %v", got)
	}
	// Paging inside the range.
	out, err := m.list(h.ctx, &listInput{CreatedFrom: day(0), Limit: 2})
	if err != nil || len(out.Body.Data) != 2 || out.Body.NextCursor == nil {
		t.Fatalf("page 1: %+v %v", out, err)
	}
	if got := list(listInput{CreatedFrom: day(0), Limit: 2, Cursor: *out.Body.NextCursor}); len(got) != 1 || got[0] != ids[0] {
		t.Fatalf("page 2: %v", got)
	}
	for _, bad := range []listInput{
		{CreatedFrom: "2026-10-20"}, {CreatedTo: "yesterday"},
		{CreatedFrom: day(2), CreatedTo: day(1)}, {CreatedFrom: day(1), CreatedTo: day(1)},
	} {
		if _, err := m.list(h.ctx, &bad); errs.CodeOf(err) != errs.CodeValidationFailed {
			t.Fatalf("%+v: %v", bad, err)
		}
	}
}
