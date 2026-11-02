package templates

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/settings"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

func TestHelpersEdgeCoverage(t *testing.T) {
	if isEmpty(0) || isEmpty(1) || !isEmpty(false) {
		t.Fatal("isEmpty bool/number")
	}
	out, err := Render(content{Text: `{{url u}}{{date bad}}{{default 0 "x"}}`}, map[string]any{"u": "", "bad": "nope"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Text != "0" && out.Text != "x" {
		// default of 0 is "0" (not empty)
		if out.Text != "0" {
			t.Fatalf("%q", out.Text)
		}
	}
	_, err = Render(content{Text: `{{url u}}`}, map[string]any{"u": "data:text/html,x"})
	if errs.CodeOf(err) != errs.CodeTemplateRenderFailed {
		t.Fatal(err)
	}
	err = ValidateSources(content{Partials: map[string]string{"bad": "{{#if"}})
	if errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	when := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	out, err = Render(content{Text: `{{date when tz="UTC"}}`}, map[string]any{"when": when.Unix()})
	if err != nil || out.Text == "" {
		t.Fatalf("%q %v", out.Text, err)
	}
}

func TestServiceErrorPathsNoDB(t *testing.T) {
	s := &Service{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if s.registry() == nil {
		t.Fatal("default registry")
	}
	ctx := context.Background()
	if err := s.requireFeature(ctx); !errors.Is(err, store.ErrNoTenant) {
		t.Fatalf("no tenant: %v", err)
	}
	org := store.NewID(store.PrefixOrganization)
	ctx = tenancy.WithCaller(ctx, tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: "u", OrganizationID: org, Role: tenancy.RoleViewer,
	})
	// Viewer cannot create (PermSend).
	_, err := s.Create(ctx, CreateInput{Name: "n", Text: "t"})
	if err == nil {
		t.Fatal("expected rbac")
	}
	m := &Module{svc: &Service{Registry: features.DefaultRegistry(), Env: "production",
		Log: slog.New(slog.NewTextHandler(io.Discard, nil))}}
	owner := tenancy.WithCaller(context.Background(), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: "u", OrganizationID: org, Role: tenancy.RoleOwner,
	})
	if _, err := m.getTemplate(owner, &templateIDIn{TemplateID: "bad"}); errs.CodeOf(err) != errs.CodeTemplateNotFound {
		t.Fatal(err)
	}
	if _, err := m.patchTemplate(owner, &patchIn{TemplateID: "bad"}); errs.CodeOf(err) != errs.CodeTemplateNotFound {
		t.Fatal(err)
	}
	if _, err := m.deleteTemplate(owner, &deleteIn{TemplateID: "bad"}); errs.CodeOf(err) != errs.CodeTemplateNotFound {
		t.Fatal(err)
	}
	if _, err := m.previewTemplate(owner, &previewIn{TemplateID: "bad"}); errs.CodeOf(err) != errs.CodeTemplateNotFound {
		t.Fatal(err)
	}
}

func TestRequireFeatureDefaultRegistry(t *testing.T) {
	s := &Service{Env: "production", Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	org := store.NewID(store.PrefixOrganization)
	ctx := tenancy.WithCaller(context.Background(), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: "u", OrganizationID: org, Role: tenancy.RoleOwner,
	})
	if err := s.requireFeature(ctx); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.RenderParts(ctx, "not-tpl", nil, nil); errs.CodeOf(err) != errs.CodeTemplateNotFound {
		t.Fatal(err)
	}
	_ = settings.DocIDGlobal
}
