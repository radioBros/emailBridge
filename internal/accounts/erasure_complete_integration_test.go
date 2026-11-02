//go:build integration

package accounts

import (
	"context"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/jobs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// TestIntegrationAccountErasureComplete seeds every account-scoped collection for two accounts,
// runs erasure on one, and asserts the target is gone while the other remains (W34).
func TestIntegrationAccountErasureComplete(t *testing.T) {
	svc, ctx, org := testSvc(t)
	a, err := svc.Create(ctx, sampleCreate("erase-complete"))
	if err != nil {
		t.Fatal(err)
	}
	other, err := svc.Create(ctx, sampleCreate("keep-complete"))
	if err != nil {
		t.Fatal(err)
	}

	seed := func(accountID string) {
		t.Helper()
		docs := []struct {
			coll string
			doc  bson.M
		}{
			{"messages", bson.M{"_id": store.NewID(store.PrefixMessage), "accountId": accountID, "organizationId": org}},
			{"archivedMessages", bson.M{"_id": "arc_" + accountID, "accountId": accountID, "organizationId": org}},
			{"mailboxes", bson.M{"_id": store.NewID(store.PrefixMailbox), "accountId": accountID, "organizationId": org}},
			{"outboundMessages", bson.M{"_id": "out_" + accountID, "accountId": accountID, "organizationId": org}},
			{"capturedMessages", bson.M{"_id": "cap_" + accountID, "accountId": accountID, "organizationId": org}},
			{"idempotencyKeys", bson.M{"_id": "idk_" + accountID, "accountId": accountID, "organizationId": org}},
			{"deliveries", bson.M{"_id": "dlv_" + accountID, "accountId": accountID, "organizationId": org}},
			{"events", bson.M{"_id": store.NewID(store.PrefixEvent), "accountId": accountID, "organizationId": org}},
			{"attachments", bson.M{"_id": "att_" + accountID, "accountId": accountID, "organizationId": org}},
			{"exports", bson.M{"_id": "exp_" + accountID, "accountId": accountID, "organizationId": org}},
			{"drafts", bson.M{"_id": "drf_" + accountID, "accountId": accountID, "organizationId": org}},
			{"deliveryTests", bson.M{"_id": "dtt_" + accountID, "accountId": accountID, "organizationId": org}},
			{"trackingLinks", bson.M{"_id": "trl_" + accountID, "accountId": accountID, "organizationId": org}},
			{"trackingEvents", bson.M{"_id": "tre_" + accountID, "accountId": accountID, "organizationId": org}},
			{"connectLinks", bson.M{"_id": "lnk_" + accountID, "accountId": accountID, "organizationId": org}},
			{"pecReceipts", bson.M{"_id": "rcp_" + accountID, "accountId": accountID, "organizationId": org, "transportId": "t-" + accountID}},
			{"msaCredentials", bson.M{"_id": accountID, "organizationId": org, "hash": "x"}},
			{"imapProxyCredentials", bson.M{"_id": accountID, "organizationId": org, "hash": "x"}},
			{"gmailSyncState", bson.M{"_id": accountID, "organizationId": org}},
			{"graphSyncState", bson.M{"_id": accountID, "organizationId": org}},
		}
		for _, d := range docs {
			if _, err := svc.DB.Collection(d.coll).InsertOne(ctx, d.doc); err != nil {
				t.Fatalf("%s: %v", d.coll, err)
			}
		}
	}
	seed(a.ID)
	seed(other.ID)

	rctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); svc.Jobs.Run(rctx, 1) }()
	defer func() { cancel(); <-done }()

	job, err := svc.Delete(ctx, a.ID, formatETag(a.Version))
	if err != nil {
		t.Fatal(err)
	}
	waitJob(t, svc, ctx, job.ID, jobs.Succeeded)

	assertGone := func(coll, key, id string) {
		t.Helper()
		n, err := svc.DB.Collection(coll).CountDocuments(ctx, bson.M{key: id})
		if err != nil || n != 0 {
			t.Fatalf("%s[%s=%s] still has %d: %v", coll, key, id, n, err)
		}
	}
	assertKept := func(coll, key, id string) {
		t.Helper()
		n, err := svc.DB.Collection(coll).CountDocuments(ctx, bson.M{key: id})
		if err != nil || n == 0 {
			t.Fatalf("%s[%s=%s] missing: n=%d err=%v", coll, key, id, n, err)
		}
	}

	for _, tgt := range tenancy.AccountEraseTargets() {
		if tgt.Coll == "events" {
			// account.deleted is emitted at the end of erasure and must remain (S11 §5.1).
			n, _ := svc.DB.Collection("events").CountDocuments(ctx, bson.M{
				"accountId": a.ID, "type": bson.M{"$ne": "account.deleted"},
			})
			if n != 0 {
				t.Fatalf("non-deleted events remain: %d", n)
			}
			assertKept("events", "accountId", other.ID)
			continue
		}
		assertGone(tgt.Coll, string(tgt.Key), a.ID)
		assertKept(tgt.Coll, string(tgt.Key), other.ID)
	}
	if err := svc.coll().FindOne(ctx, bson.M{"_id": a.ID}).Err(); err == nil {
		t.Fatal("account document remains")
	}
	if err := svc.coll().FindOne(ctx, bson.M{"_id": other.ID}).Err(); err != nil {
		t.Fatal("other account erased")
	}
}
