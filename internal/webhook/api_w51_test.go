package webhook

import (
	"context"
	"testing"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

func TestModuleReplayAPIWrappers(t *testing.T) {
	t.Parallel()
	m := &Module{service: &Service{}}
	org := store.NewID(store.PrefixOrganization)
	ctx := tenancy.WithCaller(context.Background(), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: "usr_w51replay00000000000001", OrganizationID: org,
		Role: tenancy.RoleAdmin,
	})
	_, err := m.bulkReplayDeliveries(ctx, &bulkReplayInput{Body: BulkReplayFilter{}})
	if errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("bulk: %v", err)
	}
	_, err = m.replayEventsToEndpoint(ctx, &eventsReplayInput{Body: EventsReplayRequest{}})
	if err == nil {
		t.Fatal("events replay expected error")
	}
}
