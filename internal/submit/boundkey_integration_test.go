//go:build integration

package submit

import (
	"context"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
)

// Every per-message outbox operation checks the message's account: a key bound to (or scoped to) another
// account of the same organisation cannot read, retry, send-now or cancel it.
func TestIntegrationPerMessageOpsAccountBoundKey(t *testing.T) {
	h := newHarness(t)
	otherAcc := store.NewID(store.PrefixAccount)
	foreign := h.key(h.org, otherAcc, "outbound:write")
	scopedForeign := h.key(h.org, "", "outbound:write:"+otherAcc, "outbound:read:"+otherAcc)
	own := h.key(h.org, h.acc.ID, "outbound:write")

	failed := h.submit(SubmitRequest{})
	if _, err := h.svc.Repo.coll().UpdateOne(h.ctx, bson.M{"_id": failed}, bson.M{
		"$set": bson.M{"state": StateFailed, "finalError": `{"code":"smtp_permanent","title":"x"}`},
	}); err != nil {
		t.Fatal(err)
	}
	future := h.svc.now().Add(time.Hour)
	scheduled := h.submit(SubmitRequest{SendAt: &future})

	for name, cctx := range map[string]context.Context{"bound": foreign, "scoped": scopedForeign} {
		if _, err := h.svc.Get(cctx, failed); !denied(err) {
			t.Fatalf("%s get: %v", name, err)
		}
		if _, err := h.svc.Retry(cctx, failed, ""); !denied(err) {
			t.Fatalf("%s retry: %v", name, err)
		}
		if _, err := h.svc.SendNow(cctx, scheduled, ""); !denied(err) {
			t.Fatalf("%s send-now: %v", name, err)
		}
		if _, err := h.svc.Cancel(cctx, scheduled, ""); !denied(err) {
			t.Fatalf("%s cancel: %v", name, err)
		}
	}
	if m := h.get(failed); m.State != StateFailed {
		t.Fatalf("foreign retry changed the message: %s", m.State)
	}
	if m := h.get(scheduled); m.State != StateScheduled {
		t.Fatalf("foreign send-now changed the message: %s", m.State)
	}

	if m, err := h.svc.Get(own, failed); err != nil || m.ID != failed {
		t.Fatalf("own get: %v", err)
	}
	if m, err := h.svc.Retry(own, failed, ""); err != nil || m.State != StateQueued {
		t.Fatalf("own retry: %v", err)
	}
	if m, err := h.svc.SendNow(own, scheduled, ""); err != nil || m.State != StateQueued {
		t.Fatalf("own send-now: %v", err)
	}
	// Another organisation never sees the message at all.
	other := h.key(store.NewID(store.PrefixOrganization), "", "outbound:write")
	if _, err := h.svc.Retry(other, failed, ""); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatalf("cross-tenant retry: %v", err)
	}
	if _, err := h.svc.Get(other, failed); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatalf("cross-tenant get: %v", err)
	}
}

// denied is true for the 403 answers: a bound key on another account (forbidden) or a key whose
// resource-specific scopes do not cover the account (scope_missing).
func denied(err error) bool {
	c := errs.CodeOf(err)
	return c == errs.CodeForbidden || c == errs.CodeScopeMissing
}
