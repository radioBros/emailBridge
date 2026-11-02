package connect

import (
	"context"
	"strings"
	"testing"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

func TestSanitizeEdgeCases(t *testing.T) {
	if SanitizeHeaderHTML("") != "" {
		t.Fatal("empty")
	}
	got := SanitizeHeaderHTML(`<img src="http://insecure/x.png" alt="x"><img src="https://ok/x.png" alt="y">`)
	if strings.Contains(got, "http://insecure") || !strings.Contains(got, "https://ok") {
		t.Fatalf("%q", got)
	}
	got = SanitizeHeadHTML(`<meta http-equiv="set-cookie" content="a=b"><link rel="stylesheet" href="/local.css">`)
	if strings.Contains(strings.ToLower(got), "set-cookie") || strings.Contains(got, "/local.css") {
		t.Fatalf("%q", got)
	}
	if len(StyleHashes("")) != 0 {
		t.Fatal("no styles")
	}
}

func TestUpdateBrandingConflictAndForbidden(t *testing.T) {
	st := newMemStore()
	st.orgs["org_1"] = &orgDoc{ID: "org_1", Name: "Acme", Version: 2}
	s := NewService(st, nil)
	ctx := tenancy.WithCaller(context.Background(), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: "u", OrganizationID: "org_1", Role: tenancy.RoleAdmin,
	})
	h := "<p>x</p>"
	if _, _, err := s.UpdateBranding(ctx, BrandingUpdate{HeaderHTML: &h}, `"1"`); errs.CodeOf(err) != errs.CodeVersionMismatch {
		t.Fatal(err)
	}
	if _, _, err := s.UpdateBranding(context.Background(), BrandingUpdate{HeaderHTML: &h}, ""); errs.CodeOf(err) != errs.CodeUnauthenticated {
		t.Fatal(err)
	}
	if _, err := s.PreviewBrandingHTML(context.Background(), BrandingPreviewRequest{}); errs.CodeOf(err) != errs.CodeUnauthenticated {
		t.Fatal(err)
	}
	m := &Module{svc: s}
	out, err := m.previewBranding(ctx, &previewBrandingIn{Body: BrandingPreviewRequest{HeaderHTML: `<script>x</script><p>ok</p>`}})
	if err != nil || strings.Contains(out.Body.HeaderHTML, "script") {
		t.Fatalf("%+v %v", out, err)
	}
	got, err := m.getBranding(ctx, &struct{}{})
	if err != nil || got.Body.BrandName == "" {
		t.Fatal(err)
	}
	patched, err := m.patchBranding(ctx, &patchBrandingIn{Body: BrandingUpdate{BrandName: strPtr("N")}})
	if err != nil || patched.Body.BrandName != "N" {
		t.Fatalf("%+v %v", patched, err)
	}
}

func strPtr(s string) *string { return &s }
