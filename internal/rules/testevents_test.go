package rules

import (
	"context"
	"testing"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

func TestListTestEventsBoundKeyOtherAccountForbidden(t *testing.T) {
	ctx := tenancy.WithCaller(context.Background(), tenancy.Caller{
		Kind: tenancy.CallerAPIKey, APIKeyID: "key_t", OrganizationID: "org_a", AccountID: "acc_1",
		Scopes: []string{"routes:read"},
	})
	// Rejected before any database access.
	if _, err := (&Repo{}).ListTestEvents(ctx, TestEventsFilter{AccountID: "acc_2"}); errs.CodeOf(err) != errs.CodeForbidden {
		t.Fatalf("got %v", err)
	}
}
