package exports

import (
	"context"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

func TestNormalizeInclude(t *testing.T) {
	got, err := normalizeInclude(Include{})
	if err != nil || !got.MetadataOnly {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := normalizeInclude(Include{MetadataOnly: true, Raw: true}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("%v", err)
	}
	got, err = normalizeInclude(Include{Raw: true})
	if err != nil || got.MetadataOnly || !got.Raw {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestParseSort(t *testing.T) {
	f, d, err := parseSort("")
	if err != nil || f != "createdAt" || d != -1 {
		t.Fatalf("%s %d %v", f, d, err)
	}
	f, d, err = parseSort("id")
	if err != nil || f != "_id" || d != 1 {
		t.Fatalf("%s %d %v", f, d, err)
	}
	if _, _, err := parseSort("size"); errs.CodeOf(err) != errs.CodeSortNotAllowed {
		t.Fatalf("%v", err)
	}
}

func TestCursorRoundTrip(t *testing.T) {
	p := cursorPayload{ID: "exp_1", Field: "createdAt", Desc: true, Key: "2026-01-01T00:00:00Z"}
	s := encodeCursor(p)
	got, err := decodeCursor(s)
	if err != nil || got.ID != p.ID {
		t.Fatalf("%+v %v", got, err)
	}
	filt, err := cursorFilter(got, "createdAt", -1)
	if err != nil || filt == nil {
		t.Fatalf("%v %v", filt, err)
	}
	filt, err = cursorFilter(cursorPayload{ID: "x"}, "_id", 1)
	if err != nil || filt == nil {
		t.Fatalf("%v", err)
	}
}

func TestRequireFeatureOff(t *testing.T) {
	s := &Service{Registry: features.DefaultRegistry(), Env: "production"}
	ctx := tenancy.WithCaller(store.WithOrganization(context.Background(), store.NewID(store.PrefixOrganization)), tenancy.Caller{
		Kind: tenancy.CallerUser, OrganizationID: store.NewID(store.PrefixOrganization), Role: tenancy.RoleOwner,
	})
	if err := s.requireFeature(ctx); errs.CodeOf(err) != errs.CodeFeatureDisabled {
		t.Fatalf("default off: %v", err)
	}
	s.SkipFeatureGate = true
	if err := s.requireFeature(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestSortKey(t *testing.T) {
	e := Export{ID: "exp_x", CreatedAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
	if sortKey(e, "createdAt") == nil || sortKey(e, "_id") != e.ID {
		t.Fatal("sortKey")
	}
}
