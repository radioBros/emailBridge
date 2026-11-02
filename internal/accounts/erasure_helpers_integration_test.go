//go:build integration

package accounts

import (
	"context"
	"encoding/json"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/jobs"
	"github.com/radiobros/emailbridge/internal/store"
)

func TestIntegrationErasureHelpersAndCancel(t *testing.T) {
	svc, ctx, org := testSvc(t)
	acc := store.NewID(store.PrefixAccount)
	_, _ = svc.DB.Collection("messages").InsertOne(ctx, bson.M{"_id": "m1", "accountId": acc, "organizationId": org})
	n, err := svc.deleteByAccount(ctx, "messages", acc)
	if err != nil || n != 1 {
		t.Fatalf("deleteByAccount: %d %v", n, err)
	}
	n, err = svc.deleteByAccountKey(ctx, "messages", "", acc)
	if err != nil || n != 0 {
		t.Fatalf("empty key: %d %v", n, err)
	}

	a, err := svc.Create(ctx, sampleCreate("erase-cancel"))
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(erasurePayload{AccountID: a.ID, OrganizationID: org})
	j := &jobs.Job{ID: store.NewID(store.PrefixJob), OrganizationID: org, Payload: payload}
	_, err = svc.handleErasure(ctx, j, &cancelReporter{cancelAt: 0})
	if errs.CodeOf(err) != errs.CodeConflict {
		t.Fatalf("cancel: %v", err)
	}

	_, err = svc.handleErasure(ctx, &jobs.Job{Payload: json.RawMessage(`{}`)}, &cancelReporter{cancelAt: 99})
	if errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("bad payload: %v", err)
	}

	// Org taken from job when payload omits organizationId
	b2, err := svc.Create(ctx, sampleCreate("erase-org-from-job"))
	if err != nil {
		t.Fatal(err)
	}
	payloadOnlyAcc, _ := json.Marshal(map[string]string{"accountId": b2.ID})
	res, err := svc.handleErasure(ctx, &jobs.Job{ID: store.NewID(store.PrefixJob), OrganizationID: org, Payload: payloadOnlyAcc}, &cancelReporter{cancelAt: 99})
	if err != nil {
		t.Fatal(err)
	}
	if res.(map[string]int64)["account"] != 1 {
		t.Fatalf("org-from-job: %+v", res)
	}

	// Idempotent: account already gone
	_, _ = svc.coll().DeleteOne(ctx, bson.M{"_id": a.ID})
	payload, _ = json.Marshal(erasurePayload{AccountID: a.ID, OrganizationID: org})
	res, err = svc.handleErasure(ctx, &jobs.Job{ID: store.NewID(store.PrefixJob), OrganizationID: org, Payload: payload}, &cancelReporter{cancelAt: 99})
	if err != nil {
		t.Fatal(err)
	}
	counts := res.(map[string]int64)
	if counts["account"] != 0 {
		t.Fatalf("gone account counts: %+v", counts)
	}
}

type cancelReporter struct {
	n, cancelAt int
}

func (c *cancelReporter) Progress(context.Context, int64, int64) {}
func (c *cancelReporter) Cancelled() bool {
	c.n++
	return c.n > c.cancelAt
}
