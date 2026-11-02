//go:build integration

package accounts

import (
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/events"
)

// An account no node can take gets state disconnected with reason capacity and one state_changed event;
// running accounts and repeats are left alone.
func TestIntegrationMarkUnplaced(t *testing.T) {
	svc, ctx, _ := testSvc(t)
	ss := NewSyncSource(svc)
	a, err := svc.Create(ctx, sampleCreate("cap-1"))
	if err != nil {
		t.Fatal(err)
	}
	if a.State != StateNew {
		t.Fatalf("new account state %s", a.State)
	}
	if err := ss.Unplaced(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	got, err := svc.load(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateDisconnected || got.StateReason == nil || got.StateReason.Code != ReasonCodeCapacity || got.Version != a.Version+1 {
		t.Fatalf("after Unplaced: %+v %+v", got.State, got.StateReason)
	}
	stateEvents := func() int64 {
		n, _ := svc.DB.Collection(events.Collection).CountDocuments(ctx, bson.M{"type": events.AccountStateChanged, "accountId": a.ID})
		return n
	}
	if stateEvents() != 1 {
		t.Fatalf("state_changed events: %d", stateEvents())
	}
	// Idempotent: already marked.
	if err := ss.Unplaced(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if again, _ := svc.load(ctx, a.ID); again.Version != got.Version || stateEvents() != 1 {
		t.Fatal("a repeated mark must not write again")
	}
	// A running account (connected) is never marked.
	if _, err := svc.coll().UpdateOne(ctx, bson.M{"_id": a.ID}, bson.M{"$set": bson.M{"state": StateConnected}, "$unset": bson.M{"stateReason": ""}}); err != nil {
		t.Fatal(err)
	}
	if err := ss.Unplaced(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if after, _ := svc.load(ctx, a.ID); after.State != StateConnected {
		t.Fatalf("a running account was marked: %s", after.State)
	}
	// Unknown accounts are ignored.
	if err := ss.Unplaced(ctx, "acc_01missing0000000000000000"); err != nil {
		t.Fatal(err)
	}
}
