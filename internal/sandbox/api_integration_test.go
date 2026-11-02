//go:build integration

package sandbox

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

func codeOf(err error) errs.Code {
	if err == nil {
		return ""
	}
	return errs.CodeOf(err)
}

func TestIntegrationSandboxAPI(t *testing.T) {
	h := newHarness(t, "development", false)
	h.setPolicy(true, PolicyCapture, "")
	id := h.submitMsg()
	h.work()
	list, err := h.svc.List(h.ctx, ListFilter{Text: "Sandbox", From: time.Now().Add(-time.Hour), To: time.Now().Add(time.Hour)})
	if err != nil || len(list.Data) != 1 {
		t.Fatalf("list filters: %+v (%v)", list, err)
	}
	capID := list.Data[0].ID
	got, err := h.svc.Get(h.ctx, capID)
	if err != nil || got.OutboundID != id {
		t.Fatalf("get: %+v (%v)", got, err)
	}
	_, body, err := h.svc.Raw(h.ctx, capID)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(body)
	_ = body.Close()
	if len(raw) == 0 {
		t.Fatal("empty raw")
	}
	page, err := h.svc.List(h.ctx, ListFilter{Limit: 1, IncludeTotal: true, Page: 1, Sort: "-capturedAt"})
	if err != nil || page.Total == nil || *page.Total < 1 {
		t.Fatalf("page+total: %+v (%v)", page, err)
	}
	if err := h.svc.Delete(h.ctx, capID); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.Get(h.ctx, capID); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatalf("deleted: %v", err)
	}
}

func TestIntegrationTenantIsolation(t *testing.T) {
	h := newHarness(t, "development", false)
	h.setPolicy(true, PolicyCapture, "")
	h.submitMsg()
	h.work()
	list, _ := h.svc.List(h.ctx, ListFilter{})
	capID := list.Data[0].ID
	other := h.caller(store.NewID(store.PrefixOrganization), tenancy.RoleOwner)
	if _, err := h.svc.Get(other, capID); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatalf("cross-tenant get: %v", err)
	}
	if err := h.svc.Delete(other, capID); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatalf("cross-tenant delete: %v", err)
	}
	out, err := h.svc.List(other, ListFilter{})
	if err != nil || len(out.Data) != 0 {
		t.Fatalf("cross-tenant list: %+v (%v)", out, err)
	}
}

func TestIntegrationRolesAndScopes(t *testing.T) {
	h := newHarness(t, "development", false)
	h.setPolicy(true, PolicyCapture, "")
	h.submitMsg()
	h.work()
	list, _ := h.svc.List(h.ctx, ListFilter{})
	capID := list.Data[0].ID

	for _, tc := range []struct {
		name     string
		ctx      context.Context
		canRead  bool
		canWrite bool
		codeW    errs.Code
	}{
		{"owner", h.caller(h.org, tenancy.RoleOwner), true, true, ""},
		{"admin", h.caller(h.org, tenancy.RoleAdmin), true, true, ""},
		{"operator", h.caller(h.org, tenancy.RoleOperator), true, true, ""},
		{"viewer", h.caller(h.org, tenancy.RoleViewer), true, false, errs.CodeForbidden},
		{"key sandbox:write", h.key(h.org, "", "sandbox:write"), true, true, ""},
		{"key sandbox:read", h.key(h.org, "", "sandbox:read"), true, false, errs.CodeScopeMissing},
		{"key outbound:write", h.key(h.org, "", "outbound:write"), false, true, ""}, // captures are read with sandbox:read (S10 §3)
		{"key logs:read", h.key(h.org, "", "logs:read"), false, false, errs.CodeScopeMissing},
		{"no caller", context.Background(), false, false, errs.CodeUnauthenticated},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := h.svc.Get(tc.ctx, capID)
			if tc.canRead && err != nil {
				t.Fatalf("read: %v", err)
			}
			if !tc.canRead && codeOf(err) != errs.CodeUnauthenticated && codeOf(err) != errs.CodeNotFound {
				// unauthenticated for no caller; tenant filter may 404 for others
				if codeOf(err) == "" {
					t.Fatal("read should fail")
				}
			}
			err = h.svc.Delete(tc.ctx, capID)
			if tc.canWrite {
				if err != nil {
					t.Fatalf("write: %v", err)
				}
				// re-capture for next role
				h.submitMsg()
				h.work()
				list, _ = h.svc.List(h.ctx, ListFilter{})
				capID = list.Data[len(list.Data)-1].ID
				return
			}
			if codeOf(err) != tc.codeW {
				t.Fatalf("write code %s want %s (%v)", codeOf(err), tc.codeW, err)
			}
		})
	}
}

func TestIntegrationAccountBoundKey(t *testing.T) {
	h := newHarness(t, "development", false)
	h.setPolicy(true, PolicyCapture, "")
	h.submitMsg()
	h.work()
	list, _ := h.svc.List(h.ctx, ListFilter{})
	capID := list.Data[0].ID

	otherAcc := store.NewID(store.PrefixAccount)
	bound := h.key(h.org, otherAcc, "sandbox:write")
	if _, err := h.svc.Get(bound, capID); errs.CodeOf(err) != errs.CodeForbidden {
		t.Fatalf("bound get: %v", err)
	}
	if err := h.svc.Delete(bound, capID); errs.CodeOf(err) != errs.CodeForbidden {
		t.Fatalf("bound delete: %v", err)
	}
	own := h.key(h.org, h.acc.ID, "sandbox:read")
	list, err := h.svc.List(own, ListFilter{})
	if err != nil || len(list.Data) != 1 {
		t.Fatalf("own list: %+v (%v)", list, err)
	}
	list, err = h.svc.List(bound, ListFilter{})
	if err != nil || len(list.Data) != 0 {
		t.Fatalf("other bound list: %+v (%v)", list, err)
	}
	if _, err := h.svc.List(bound, ListFilter{AccountID: h.acc.ID}); errs.CodeOf(err) != errs.CodeForbidden {
		t.Fatalf("bound asking for another account: %v", err)
	}
}
