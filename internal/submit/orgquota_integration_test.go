//go:build integration

package submit

import (
	"context"
	"strconv"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// TestIntegrationOrgQuotas: organisation quotas (owner decision 2026-10-27) are enforced in addition to the
// instance EB_QUOTA_* limits — unset = unlimited, the stricter wins (the organisation on a tie), a reached
// quota is 429 organization_quota_exceeded with Retry-After + RateLimit-*, and one organisation's quota
// never limits another.
func TestIntegrationOrgQuotas(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	setQuotas := func(org string, q bson.M) {
		t.Helper()
		if _, err := h.db.Collection(tenancy.OrganizationsCollection).UpdateOne(ctx, bson.M{"_id": org},
			bson.M{"$set": bson.M{"quotas": q}}, options.UpdateOne().SetUpsert(true)); err != nil {
			t.Fatal(err)
		}
	}
	send := func(c context.Context, accountID string) error {
		_, err := h.svc.Submit(c, accountID, SubmitRequest{To: []AddressInput{{Address: testTo}}, Text: "quota"}, "", false)
		return err
	}

	// Unset (no organisation document, then an empty quotas object, then explicit 0) = unlimited.
	for range 3 {
		h.submit(SubmitRequest{})
	}
	setQuotas(h.org, bson.M{})
	h.submit(SubmitRequest{})
	setQuotas(h.org, bson.M{tenancy.QuotaSendsPerDay: 0, tenancy.QuotaSendsPerAccountMinute: 0})
	h.submit(SubmitRequest{}) // 5 sent so far

	// Organisation daily quota reached.
	setQuotas(h.org, bson.M{tenancy.QuotaSendsPerDay: 5})
	err := send(h.ctx, h.acc.ID)
	if errs.CodeOf(err) != errs.CodeOrganizationQuotaExceeded {
		t.Fatalf("org daily quota: %v", err)
	}
	p := errs.ProblemFrom(err, "", "")
	hd := p.GetHeaders()
	if p.Status != 429 || hd.Get("RateLimit-Limit") != "5" || hd.Get("RateLimit-Remaining") != "0" {
		t.Fatalf("problem %d headers %v", p.Status, hd)
	}
	if ra, convErr := strconv.Atoi(hd.Get("Retry-After")); convErr != nil || ra < 1 || ra > 24*3600 {
		t.Fatalf("Retry-After %q", hd.Get("Retry-After"))
	}

	// Cross-organisation: org B has its own account and no quotas — unaffected by org A's limit.
	orgB := store.NewID(store.PrefixOrganization)
	accB := *h.acc
	accB.ID, accB.OrganizationID = store.NewID(store.PrefixAccount), orgB
	h.accounts.put(&accB)
	ctxB := tenancy.WithCaller(ctx, tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: store.NewID(store.PrefixUser), OrganizationID: orgB, Role: tenancy.RoleOwner,
	})
	if err := send(ctxB, accB.ID); err != nil {
		t.Fatalf("org B blocked by org A's quota: %v", err)
	}
	// Org B per-account-minute quota; org A's account is not counted against it.
	setQuotas(orgB, bson.M{tenancy.QuotaSendsPerAccountMinute: 1})
	if err := send(ctxB, accB.ID); errs.CodeOf(err) != errs.CodeOrganizationQuotaExceeded {
		t.Fatalf("org B per-minute quota: %v", err)
	}

	// The stricter limit wins: instance 3 < organisation 100 → the instance code; a tie → the organisation.
	setQuotas(h.org, bson.M{tenancy.QuotaSendsPerDay: 100})
	h.svc.QuotaSendsPerDay = 3
	if err := send(h.ctx, h.acc.ID); errs.CodeOf(err) != errs.CodeQuotaSendsExceeded {
		t.Fatalf("instance stricter: %v", err)
	}
	h.svc.QuotaSendsPerDay = 100
	setQuotas(h.org, bson.M{tenancy.QuotaSendsPerDay: 100, tenancy.QuotaSendsPerAccountMinute: 3})
	h.svc.QuotaSendsPerMinute = 3
	if err := send(h.ctx, h.acc.ID); errs.CodeOf(err) != errs.CodeOrganizationQuotaExceeded {
		t.Fatalf("tie goes to the organisation: %v", err)
	}
	// Lifting both limits lets the organisation send again.
	h.svc.QuotaSendsPerDay, h.svc.QuotaSendsPerMinute = 0, 0
	setQuotas(h.org, bson.M{})
	if err := send(h.ctx, h.acc.ID); err != nil {
		t.Fatalf("after lifting: %v", err)
	}
}
