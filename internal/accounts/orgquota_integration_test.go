//go:build integration

package accounts

import (
	"context"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// TestIntegrationOrgAccountQuota: the organisation `accounts` quota is enforced in addition to
// EB_QUOTA_ACCOUNTS (stricter wins), unset = unlimited, per organisation only, and its 429 carries no
// Retry-After (waiting never frees a slot).
func TestIntegrationOrgAccountQuota(t *testing.T) {
	svc, ctx, org := testSvc(t)
	setQuotas := func(o string, q bson.M) {
		t.Helper()
		if _, err := svc.DB.Collection(tenancy.OrganizationsCollection).UpdateOne(context.Background(), bson.M{"_id": o},
			bson.M{"$set": bson.M{"quotas": q}}, options.UpdateOne().SetUpsert(true)); err != nil {
			t.Fatal(err)
		}
	}
	// Unset = unlimited.
	for _, ext := range []string{"a1", "a2"} {
		if _, err := svc.Create(ctx, sampleCreate(ext)); err != nil {
			t.Fatalf("unlimited create %s: %v", ext, err)
		}
	}
	setQuotas(org, bson.M{tenancy.QuotaAccounts: 2})
	_, err := svc.Create(ctx, sampleCreate("a3"))
	if errs.CodeOf(err) != errs.CodeOrganizationQuotaExceeded {
		t.Fatalf("org account quota: %v", err)
	}
	if p := errs.ProblemFrom(err, "", ""); p.Status != 429 || p.GetHeaders().Get("Retry-After") != "" {
		t.Fatalf("problem %d headers %v", p.Status, p.GetHeaders())
	}

	// Another organisation is not limited by this one's quota.
	orgB := store.NewID(store.PrefixOrganization)
	ctxB := tenancy.WithCaller(context.Background(), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: store.NewID(store.PrefixUser), OrganizationID: orgB, Role: tenancy.RoleOwner,
	})
	for _, ext := range []string{"b1", "b2", "b3"} {
		if _, err := svc.Create(ctxB, sampleCreate(ext)); err != nil {
			t.Fatalf("org B create %s: %v", ext, err)
		}
	}

	// Stricter wins: instance 1 < org 10 → the instance code.
	setQuotas(org, bson.M{tenancy.QuotaAccounts: 10})
	svc.QuotaAccounts = 1
	if _, err := svc.Create(ctx, sampleCreate("a4")); errs.CodeOf(err) != errs.CodeQuotaAccountsExceeded {
		t.Fatalf("instance stricter: %v", err)
	}
	svc.QuotaAccounts = 0
	if _, err := svc.Create(ctx, sampleCreate("a5")); err != nil {
		t.Fatalf("under the org quota: %v", err)
	}
}
