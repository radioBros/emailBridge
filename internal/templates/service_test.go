package templates

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

func TestRequireFeatureDefaultOn(t *testing.T) {
	s := &Service{Registry: features.DefaultRegistry(), Env: "production",
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: func() time.Time { return time.Unix(1, 0).UTC() }}
	org := store.NewID(store.PrefixOrganization)
	ctx := tenancy.WithCaller(context.Background(), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: "usr_1", OrganizationID: org, Role: tenancy.RoleOwner,
	})
	if err := s.requireFeature(ctx); err != nil {
		t.Fatal(err)
	}
	_, err := s.Get(ctx, "not-a-tpl")
	if errs.CodeOf(err) != errs.CodeTemplateNotFound {
		t.Fatalf("bad id: %v", err)
	}
	if err := s.Delete(ctx, "not-a-tpl", ""); errs.CodeOf(err) != errs.CodeTemplateNotFound {
		t.Fatalf("delete bad id: %v", err)
	}
}

func TestParseSortAndCursorHelpers(t *testing.T) {
	f, d, err := parseSort("-name")
	if err != nil || f != "name" || d != -1 {
		t.Fatalf("%s %d %v", f, d, err)
	}
	if _, _, err := parseSort("size"); errs.CodeOf(err) != errs.CodeSortNotAllowed {
		t.Fatal(err)
	}
	tok := encodeCursor(cursorPayload{ID: "tpl_1", Field: "_id", Desc: true})
	cur, err := decodeCursor(tok)
	if err != nil || cur.ID != "tpl_1" {
		t.Fatal(err)
	}
	if _, err := decodeCursor("%%%"); err == nil {
		t.Fatal("bad cursor")
	}
	if _, err := normalizeListQ(stringsRepeat("q", 201)); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal("q too long")
	}
	tpl := Template{ID: "tpl_1", Name: "n", CreatedAt: time.Unix(1, 0).UTC()}
	if sortKey(tpl, "name") != "n" || sortKey(tpl, "_id") != "tpl_1" {
		t.Fatal("sortKey")
	}
}

func TestContentOfSnapshotClone(t *testing.T) {
	t0 := &Template{
		ID: "tpl_1", OrganizationID: "org_1", Name: "n", Subject: "s", Text: "t", HTML: "h",
		Partials: map[string]string{"a": "b"}, Strict: true, Locale: "en", Timezone: "UTC", CurrentVersion: 2,
	}
	c := contentOf(t0)
	if c.Subject != "s" || !c.Strict {
		t.Fatal(c)
	}
	v := snapshot(t0, "usr", time.Unix(1, 0).UTC())
	if v.ID != "tpl_1:v2" || contentOfVersion(v).Text != "t" {
		t.Fatal(v)
	}
	if cloneMap(nil) != nil || cloneMap(map[string]string{"x": "y"})["x"] != "y" {
		t.Fatal("cloneMap")
	}
}
