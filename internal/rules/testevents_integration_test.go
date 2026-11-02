//go:build integration

package rules

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// An account-bound API key sees only its own account's test events; org-wide keys and users see the org.
func TestIntegrationTestEventsAccountBoundKey(t *testing.T) {
	r, db := w73Repo(t)
	orgA, orgB := store.NewID(store.PrefixOrganization), store.NewID(store.PrefixOrganization)
	acc1, acc2 := store.NewID(store.PrefixAccount), store.NewID(store.PrefixAccount)
	now := time.Now().UTC()
	insert := func(id, org, acc string) {
		raw, _ := json.Marshal(map[string]any{"subject": "s-" + id})
		if _, err := db.Collection("events").InsertOne(context.Background(), bson.M{
			"_id": id, "organizationId": org, "accountId": acc, "type": "message.received", "time": now,
			"data": string(raw), "schema": 1,
		}); err != nil {
			t.Fatal(err)
		}
	}
	insert("evt_a1", orgA, acc1)
	insert("evt_a2", orgA, acc2)
	insert("evt_b1", orgB, acc1) // same account id in another org must never appear

	key := func(org, acc string) context.Context {
		return tenancy.WithCaller(context.Background(), tenancy.Caller{
			Kind: tenancy.CallerAPIKey, APIKeyID: "key_t", OrganizationID: org, AccountID: acc,
			Scopes: []string{"routes:read"},
		})
	}
	bound := key(orgA, acc1)
	list, err := r.ListTestEvents(bound, TestEventsFilter{})
	if err != nil || len(list.Data) != 1 || list.Data[0].ID != "evt_a1" {
		t.Fatalf("bound key = %+v %v", list, err)
	}
	if _, err := r.ListTestEvents(bound, TestEventsFilter{AccountID: acc2}); errs.CodeOf(err) != errs.CodeForbidden {
		t.Fatalf("other account: %v", err)
	}
	if list, err := r.ListTestEvents(bound, TestEventsFilter{AccountID: acc1}); err != nil || len(list.Data) != 1 {
		t.Fatalf("own account filter = %+v %v", list, err)
	}
	orgWide, err := r.ListTestEvents(key(orgA, ""), TestEventsFilter{})
	if err != nil || len(orgWide.Data) != 2 {
		t.Fatalf("org-wide key = %+v %v", orgWide, err)
	}
	for _, row := range orgWide.Data {
		if row.ID == "evt_b1" {
			t.Fatal("cross-tenant leak")
		}
	}
	user, err := r.ListTestEvents(asOrgW73(orgA, tenancy.RoleAdmin), TestEventsFilter{})
	if err != nil || len(user.Data) != 2 {
		t.Fatalf("user = %+v %v", user, err)
	}
}
