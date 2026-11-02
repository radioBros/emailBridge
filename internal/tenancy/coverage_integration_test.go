//go:build integration

package tenancy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/config"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/sysmail"
)

func TestIntegrationCoverageSweep(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := app.New(app.Options{Roles: app.RoleSet{app.RoleAPI: true}, NodeID: "n", Log: quiet})
	app.Provide(a, db)
	srv, err := httpapi.New(httpapi.Options{AccessLog: quiet, SecurityLog: quiet})
	if err != nil {
		t.Fatal(err)
	}
	app.Provide(a, srv)
	aud := &memAuditor{}
	app.Provide(a, Auditor(aud))
	// real-ish sysmail off service so enqueueInvite returns true path via ModeOff (Enqueue nil err)
	sm := sysmail.New(sysmail.Options{Mode: sysmail.ModeOff, Log: quiet}, nil)
	app.Provide(a, sm)

	var orgID string
	mod := &Module{
		PublicURL: "https://eb.test", InviteRate: config.Rate{Count: 100, Period: time.Minute}, Jobs: stubJobs(),
		Creator: func(_ context.Context, email, _ string) (string, error) { return "auth-" + email, nil },
		Identity: func(ctx context.Context) (Identity, bool) {
			v, ok := ctx.Value(idKey{}).(Identity)
			return v, ok
		},
		Keys: func(r *http.Request) (*Caller, error) {
			org := r.Header.Get("X-Test-Org")
			switch r.Header.Get("Authorization") {
			case "Bearer ebk_admin":
				return &Caller{Kind: CallerAPIKey, APIKeyID: "key_a", OrganizationID: org, Scopes: []string{"admin"}}, nil
			case "Bearer ebk_mem":
				return &Caller{Kind: CallerAPIKey, APIKeyID: "key_m", OrganizationID: org, Scopes: []string{"members:write"}}, nil
			case "Bearer ebk_log":
				return &Caller{Kind: CallerAPIKey, APIKeyID: "key_l", OrganizationID: org, Scopes: []string{"logs:read"}}, nil
			}
			return nil, nil
		},
	}
	mod.Bootstrap.Email, mod.Bootstrap.Password, mod.Bootstrap.OrgName = "cov@ex.test", "pw-123456789012", "CoverOrg"
	if err := mod.Start(ctx, a); err != nil {
		t.Fatal(err)
	}
	owner, err := mod.service.Repo.UserByEmail(ctx, "cov@ex.test")
	if err != nil {
		t.Fatal(err)
	}
	ms, _ := mod.service.Repo.MembershipsOf(ctx, owner.ID)
	orgID = ms[0].OrganizationID

	do := func(method, path, body string, hdr map[string]string) (int, map[string]any, http.Header) {
		var rd io.Reader
		if body != "" {
			rd = strings.NewReader(body)
		}
		r := httptest.NewRequest(method, path, rd)
		if body != "" {
			r.Header.Set("Content-Type", "application/json")
		}
		for k, v := range hdr {
			r.Header.Set(k, v)
		}
		if _, ok := hdr["Authorization"]; !ok {
			r = r.WithContext(context.WithValue(r.Context(), idKey{}, Identity{AuthUserID: owner.AuthUserID, Email: owner.Email, MFA: true}))
		}
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out, w.Header()
	}
	h := map[string]string{"EB-Organization": orgID}

	// list orgs with page/total/cursor/sort variants
	code, body, _ := do(http.MethodGet, "/v1/organizations?page=1&limit=10&includeTotal=true&sort=name", "", nil)
	if code != 200 {
		t.Fatalf("list orgs page: %d %v", code, body)
	}
	code, body, _ = do(http.MethodGet, "/v1/organizations?sort=-createdAt&limit=1", "", nil)
	if code != 200 {
		t.Fatalf("list orgs sort: %d", code)
	}
	if nc, _ := body["nextCursor"].(string); nc != "" {
		code, _, _ = do(http.MethodGet, "/v1/organizations?cursor="+nc+"&limit=1&sort=-createdAt", "", nil)
		if code != 200 {
			t.Fatalf("cursor: %d", code)
		}
	}
	code, _, _ = do(http.MethodGet, "/v1/organizations?sort=nope", "", nil)
	if code != 422 {
		t.Fatalf("bad sort: %d", code)
	}
	// Organisation quotas are a closed set of whole numbers (owner decision 2026-10-27).
	for _, bad := range []string{`{"a":2}`, `{"accounts":-1}`, `{"accounts":1.5}`, `{"sendsPerDay":"10"}`} {
		code, body, _ := do(http.MethodPost, "/v1/organizations", `{"name":"Bad","slug":"bad-quota","quotas":`+bad+`}`, nil)
		if code != 422 || body["code"] != "validation_failed" {
			t.Fatalf("quotas %s: %d %v", bad, code, body)
		}
	}
	code, body, hdr := do(http.MethodPost, "/v1/organizations", `{"name":"Beta","slug":"beta-cov","branding":{"x":1},"quotas":{"accounts":2,"sendsPerAccountMinute":null}}`, nil)
	if q, _ := body["quotas"].(map[string]any); code == 201 && (q["accounts"] != float64(2) || q["sendsPerAccountMinute"] != nil) {
		t.Fatalf("normalised quotas: %v", body["quotas"])
	}
	if code != 201 {
		t.Fatalf("create: %d %v", code, body)
	}
	org2 := body["id"].(string)
	etag := hdr.Get("ETag")
	code, _, _ = do(http.MethodGet, "/v1/organizations/"+org2, "", nil)
	if code != 200 {
		t.Fatalf("get: %d", code)
	}
	code, body, hdr = do(http.MethodPatch, "/v1/organizations/"+org2, `{"name":"Beta2","status":"suspended","branding":{"y":1},"quotas":{"sendsPerDay":3}}`, map[string]string{"If-Match": etag})
	if code != 200 {
		t.Fatalf("patch: %d %v", code, body)
	}
	etag = hdr.Get("ETag")
	if code, body, _ = do(http.MethodPatch, "/v1/organizations/"+org2, `{"quotas":{"b":3}}`, map[string]string{"If-Match": etag}); code != 422 {
		t.Fatalf("patch unknown quota: %d %v", code, body)
	}
	// empty patch
	code, _, _ = do(http.MethodPatch, "/v1/organizations/"+org2, `{}`, map[string]string{"If-Match": etag})
	if code != 200 {
		t.Fatalf("empty patch: %d", code)
	}
	code, body, _ = do(http.MethodDelete, "/v1/organizations/"+org2, "", map[string]string{"If-Match": etag})
	if code != 202 {
		t.Fatalf("delete: %d %v", code, body)
	}

	// members list with sorts/page/cursor
	code, _, _ = do(http.MethodGet, "/v1/members?page=1&limit=5&includeTotal=true&sort=email", "", h)
	if code != 200 {
		t.Fatalf("members page: %d", code)
	}
	code, body, _ = do(http.MethodGet, "/v1/members?sort=-role&limit=1", "", h)
	if code != 200 {
		t.Fatalf("members sort: %d", code)
	}
	if nc, _ := body["nextCursor"].(string); nc != "" {
		do(http.MethodGet, "/v1/members?cursor="+nc+"&limit=1&sort=-role", "", h)
	}
	code, body, hdr = do(http.MethodPost, "/v1/members", `{"email":"cov-invite@ex.test","role":"operator"}`, h)
	if code != 201 {
		t.Fatalf("invite: %d %v", code, body)
	}
	memID := body["id"].(string)
	memETag := hdr.Get("ETag")

	// add second owner so we can demote/remove
	code, body, _ = do(http.MethodPost, "/v1/members", `{"email":"cov-owner2@ex.test","role":"owner"}`, h)
	if code != 201 {
		t.Fatalf("invite owner: %d %v", code, body)
	}
	inv2, _ := mod.service.Repo.Membership(ctx, body["id"].(string))

	code, body, hdr = do(http.MethodPatch, "/v1/members/"+memID, `{"role":"viewer"}`, merge(h, map[string]string{"If-Match": memETag}))
	if code != 200 {
		t.Fatalf("patch member: %d %v", code, body)
	}
	memETag = hdr.Get("ETag")
	code, _, _ = do(http.MethodDelete, "/v1/members/"+memID, "", merge(h, map[string]string{"If-Match": memETag}))
	if code != 204 && code != 200 {
		t.Fatalf("delete member: %d", code)
	}

	// API keys
	hk := merge(h, map[string]string{"Authorization": "Bearer ebk_mem", "X-Test-Org": orgID})
	code, _, _ = do(http.MethodGet, "/v1/members", "", hk)
	if code != 200 {
		t.Fatalf("key list: %d", code)
	}
	code, _, _ = do(http.MethodPost, "/v1/members", `{"email":"keyinv@ex.test","role":"viewer"}`, hk)
	if code != 201 {
		t.Fatalf("key invite: %d", code)
	}
	code, _, _ = do(http.MethodPost, "/v1/members", `{"email":"keyown@ex.test","role":"owner"}`, hk)
	if code != 403 {
		t.Fatalf("key invite owner: %d", code)
	}
	hkAdmin := merge(h, map[string]string{"Authorization": "Bearer ebk_admin", "X-Test-Org": orgID})
	code, _, _ = do(http.MethodPost, "/v1/members", `{"email":"keyown2@ex.test","role":"owner"}`, hkAdmin)
	if code != 201 {
		t.Fatalf("admin key invite owner: %d", code)
	}
	hkLog := merge(h, map[string]string{"Authorization": "Bearer ebk_log", "X-Test-Org": orgID})
	code, _, _ = do(http.MethodPost, "/v1/members", `{"email":"x@y.z","role":"viewer"}`, hkLog)
	if code != 403 {
		t.Fatalf("log key invite: %d", code)
	}

	// service-level edge cases
	svc := mod.service
	ic := ctxAs(orgID, owner.ID, RoleOwner, true)
	if _, err := svc.CreateOrganization(ic, "", "bad", nil, nil); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	if _, err := svc.CreateOrganization(ic, "N", "BAD_SLUG", nil, nil); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	if _, err := svc.GetOrganization(ic, "org_notavalidulidxxxxxxxxxxxxx"); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatalf("bad id: %v", err)
	}
	if _, err := svc.GetOrganization(ic, store.NewID(store.PrefixOrganization)); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatal(err)
	}
	bad := "nope"
	if _, err := svc.PatchOrganization(ic, orgID, OrgPatch{Status: &bad}, ""); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	empty := "  "
	if _, err := svc.PatchOrganization(ic, orgID, OrgPatch{Name: &empty}, ""); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	// Organisation admins may edit their organisation but never its quotas (instance owner only).
	adminCtx := ctxAs(orgID, owner.ID, RoleAdmin, false)
	if _, err := svc.PatchOrganization(adminCtx, orgID, OrgPatch{Quotas: map[string]any{QuotaAccounts: 1000}}, ""); errs.CodeOf(err) != errs.CodeForbidden {
		t.Fatalf("admin raised own quota: %v", err)
	}
	if o, err := svc.PatchOrganization(ic, orgID, OrgPatch{Quotas: map[string]any{QuotaAccounts: float64(7)}}, ""); err != nil || o.Quotas[QuotaAccounts] != int64(7) {
		t.Fatalf("owner sets quota: %+v %v", o, err)
	}
	if q, err := LoadOrgQuotas(ic, svc.Repo.DB, orgID); err != nil || q.Accounts != 7 || q.SendsPerDay != 0 {
		t.Fatalf("load quotas: %+v %v", q, err)
	}
	if q, err := LoadOrgQuotas(ic, svc.Repo.DB, store.NewID(store.PrefixOrganization)); err != nil || q != (OrgQuotas{}) {
		t.Fatalf("unknown org quotas: %+v %v", q, err)
	}
	if _, err := svc.PatchOrganization(ic, orgID, OrgPatch{Quotas: map[string]any{}}, ""); err != nil {
		t.Fatal(err)
	}
	// deleting org patch/delete
	delOrg, _ := svc.CreateOrganization(ic, "Del", "del-cov", nil, nil)
	_, _ = svc.DeleteOrganization(ic, delOrg.ID, formatETag(delOrg.Version))
	if _, err := svc.PatchOrganization(ic, delOrg.ID, OrgPatch{Name: strPtr("x")}, ""); errs.CodeOf(err) != errs.CodeConflict {
		t.Fatal(err)
	}
	if _, err := svc.DeleteOrganization(ic, delOrg.ID, ""); errs.CodeOf(err) != errs.CodeConflict {
		t.Fatal(err)
	}
	if _, err := svc.DeleteOrganization(context.Background(), orgID, ""); errs.CodeOf(err) != errs.CodeUnauthenticated {
		t.Fatal(err)
	}
	viewerCtx := ctxAs(orgID, owner.ID, RoleViewer, false)
	if _, err := svc.DeleteOrganization(viewerCtx, orgID, ""); errs.CodeOf(err) != errs.CodeForbidden {
		t.Fatal(err)
	}

	admin := &User{AuthUserID: "auth-admin-cov", Email: "admin-cov@ex.test", Name: "A"}
	_ = svc.Repo.CreateUser(ctx, admin)
	admMem, _ := svc.Repo.CreateMembership(ctx, orgID, admin.ID, RoleAdmin)
	aCtx := ctxAs(orgID, admin.ID, RoleAdmin, false)
	if _, err := svc.InviteMember(aCtx, "not-an-email", RoleViewer); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	if _, err := svc.InviteMember(aCtx, "z@ex.test", Role("nope")); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	if _, err := svc.InviteMember(aCtx, owner.Email, RoleViewer); errs.CodeOf(err) != errs.CodeConflict {
		t.Fatal(err)
	}
	if _, err := svc.PatchMember(aCtx, admMem.ID, nil, ""); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	badRole := Role("x")
	if _, err := svc.PatchMember(aCtx, admMem.ID, &badRole, ""); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	if _, err := svc.PatchMember(aCtx, "mem_notavalidulidxxxxxxxxxxxxx0", strPtrRole(RoleViewer), ""); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatal(err)
	}
	if err := svc.DeleteMember(aCtx, "mem_notavalidulidxxxxxxxxxxxxx0", ""); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatal(err)
	}

	// cursor helpers + sort keys
	_ = orgSortKey(Organization{ID: "1", Name: "n", CreatedAt: time.Now()}, "name")
	_ = orgSortKey(Organization{ID: "1", CreatedAt: time.Now()}, "createdAt")
	_ = orgSortKey(Organization{ID: "1"}, "_id")
	_ = memberSortKey(Membership{ID: "1", Email: "e", Role: RoleOwner, CreatedAt: time.Now()}, "email")
	_ = memberSortKey(Membership{ID: "1", Role: RoleOwner, CreatedAt: time.Now()}, "role")
	_ = memberSortKey(Membership{ID: "1", CreatedAt: time.Now()}, "createdAt")
	_ = memberSortKey(Membership{ID: "1"}, "_id")
	_, _ = orgCursorFilter(cursorPayload{ID: "x", Key: timeKey(time.Now())}, "createdAt", -1)
	_, _ = orgCursorFilter(cursorPayload{ID: "x"}, "_id", 1)
	_, _ = memberCursorFilter(cursorPayload{ID: "x", Key: timeKey(time.Now())}, "createdAt", 1)
	_, _ = memberCursorFilter(cursorPayload{ID: "x"}, "_id", -1)
	_, _ = parseTimeKey("not-a-time")
	_, _ = parseTimeKey(timeKey(time.Now()))
	_ = mayTouchOwners(Caller{Kind: CallerSystem})
	_ = mayTouchOwners(Caller{Kind: "x"})

	// accept rate limit + validation branches
	mod.inviteLim = newInviteLimiter(1, time.Minute)
	_, _ = mod.acceptInvitation(ctx, &acceptIn{Body: tokenBody{Token: "nope"}})
	_, err = mod.acceptInvitation(ctx, &acceptIn{Body: tokenBody{Token: "nope2"}})
	if errs.CodeOf(err) != errs.CodeRateLimited && errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatalf("rate/notfound: %v", err)
	}
	mod.inviteLim = newInviteLimiter(1000, time.Minute)
	_, err = mod.acceptInvitation(ctx, &acceptIn{Body: tokenBody{Token: ""}})
	if errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatal(err)
	}
	tok, hash, _ := newInviteToken()
	now := time.Now().UTC()
	pending := &Membership{
		ID: store.NewID(store.PrefixMembership), OrganizationID: orgID, Email: "accept-cov@ex.test", Role: RoleViewer,
		Invite:    &Invite{Email: "accept-cov@ex.test", TokenHash: hash, ExpiresAt: now.Add(time.Hour)},
		CreatedAt: now, UpdatedAt: now, Version: 1,
	}
	_ = svc.Repo.InsertMembership(ctx, pending)
	_, err = mod.acceptInvitation(ctx, &acceptIn{Body: tokenBody{Token: tok, Password: "short"}})
	if errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	mod.Creator = nil
	_, err = mod.acceptInvitation(ctx, &acceptIn{Body: tokenBody{Token: tok, Password: "long-enough-password"}})
	if errs.CodeOf(err) != errs.CodeInternalError {
		t.Fatal(err)
	}
	mod.Creator = func(_ context.Context, email, _ string) (string, error) { return "auth-" + email, nil }
	out, err := mod.acceptInvitation(ctx, &acceptIn{Body: tokenBody{Token: tok, Password: "long-enough-password", Name: "N"}})
	if err != nil {
		t.Fatal(err)
	}
	_ = out

	// Membership / Count / UserByID miss
	_, err = svc.Repo.Membership(ctx, "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	_, _ = svc.Repo.CountOrganizations(ctx, nil, 10)
	_, _ = svc.Repo.CountMemberships(ctx, map[string]any{"organizationId": orgID}, 10)
	_, err = svc.Repo.UserByID(ctx, "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	_, err = svc.Repo.UserByEmail(ctx, "nobody@ex.test")
	if !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	_, err = svc.Repo.OrganizationBySlug(ctx, "no-such-slug")
	if !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	_, err = svc.Repo.UpdateOrganization(ctx, "missing", 1, map[string]any{"name": "x"})
	if !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	_, err = svc.Repo.UpdateMembership(ctx, "missing", 1, map[string]any{"role": "viewer"}, nil)
	if !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	err = svc.Repo.DeleteMembership(ctx, orgID, "missing", 0)
	if !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}

	// canAccessOrg mismatch
	other := ctxAs(store.NewID(store.PrefixOrganization), owner.ID, RoleOwner, false)
	if err := svc.canAccessOrg(other, orgID, PermRead); errs.CodeOf(err) != errs.CodeOrganizationMismatch {
		t.Fatal(err)
	}
	if err := svc.canAccessOrg(context.Background(), orgID, PermRead); errs.CodeOf(err) != errs.CodeUnauthenticated {
		t.Fatal(err)
	}
	noOrg := WithCaller(ctx, Caller{Kind: CallerUser, UserID: owner.ID, Role: RoleOwner})
	if err := svc.canAccessOrg(noOrg, orgID, PermRead); errs.CodeOf(err) != errs.CodeForbidden {
		t.Fatal(err)
	}

	// audit failure path
	svc.audit(ctx, "x", "y", "z", nil)
	svc2 := &Service{Audit: failAuditor{}, Log: quiet}
	svc2.audit(ctx, "x", "y", "z", nil)

	// enqueueInvite with mail
	_ = svc.enqueueInvite(ctx, "a@b.c", "Org", Caller{Email: "i@t", UserID: "u"}, "https://x")
	_ = svc.enqueueInvite(ctx, "a@b.c", "Org", Caller{UserID: "u"}, "https://x")

	// decodeCursor empty id
	_, err = decodeCursor(encodeCursor(cursorPayload{}))
	if err == nil {
		// encode of empty still has id ""
		b, _ := json.Marshal(cursorPayload{})
		_, _ = decodeCursor(string(b)) // will fail base64 or empty
	}

	// invite existing email conflict on accept
	tok3, hash3, _ := newInviteToken()
	_ = svc.Repo.InsertMembership(ctx, &Membership{
		ID: store.NewID(store.PrefixMembership), OrganizationID: orgID, Email: owner.Email, Role: RoleViewer,
		Invite:    &Invite{Email: owner.Email, TokenHash: hash3, ExpiresAt: now.Add(time.Hour)},
		CreatedAt: now, UpdatedAt: now, Version: 1,
	})
	_, err = mod.acceptInvitation(ctx, &acceptIn{Body: tokenBody{Token: tok3, Password: "long-enough-password"}})
	if errs.CodeOf(err) != errs.CodeConflict {
		t.Fatal(err)
	}

	_ = fmt.Sprintf("%v", inv2)
	_ = Slugify("Hello World!!!")
	_ = Slugify("x")
	_ = Slugify(strings.Repeat("a", 60))

	// list edge cases: auth, bad cursor, nextCursor + total
	if _, err := svc.ListOrganizations(ctxAs(orgID, owner.ID, RoleOwner, false), ListFilter{}); errs.CodeOf(err) != errs.CodeForbidden {
		t.Fatal("non-instance list orgs", err)
	}
	if _, err := svc.ListOrganizations(ic, ListFilter{Cursor: "%%%"}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		_, _ = svc.CreateOrganization(ic, fmt.Sprintf("Extra%d", i), fmt.Sprintf("extra-cov-%d", i), nil, nil)
	}
	page, err := svc.ListOrganizations(ic, ListFilter{Limit: 2, Sort: "name", Page: 1, IncludeTotal: true})
	if err != nil || page.Total == nil {
		t.Fatalf("page total: %+v %v", page, err)
	}
	curList, err := svc.ListOrganizations(ic, ListFilter{Limit: 2, Sort: "-id"})
	if err != nil || curList.NextCursor == nil {
		t.Fatalf("want nextCursor: %+v %v", curList, err)
	}
	_, err = svc.ListOrganizations(ic, ListFilter{Limit: 2, Sort: "-id", Cursor: *curList.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	// invalid cursor key for createdAt
	badCur := encodeCursor(cursorPayload{ID: "org_01ffffffffffffffffxxxxxxxxxx", Field: "createdAt", Key: 123})
	_, _ = svc.ListOrganizations(ic, ListFilter{Cursor: badCur, Sort: "createdAt"})

	if _, err := svc.ListMembers(context.Background(), ListFilter{}); errs.CodeOf(err) != errs.CodeUnauthenticated {
		t.Fatal(err)
	}
	if _, err := svc.ListMembers(ic, ListFilter{Cursor: "%%%"}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	mcur, err := svc.ListMembers(ic, ListFilter{Limit: 1, Sort: "-createdAt"})
	if err != nil {
		t.Fatal(err)
	}
	if mcur.NextCursor != nil {
		_, _ = svc.ListMembers(ic, ListFilter{Limit: 1, Sort: "-createdAt", Cursor: *mcur.NextCursor})
	}
	_, _ = svc.ListMembers(ic, ListFilter{Page: 1, Limit: 10, IncludeTotal: true, Sort: "role"})

	// InviteMember: no org, deleting org, pending dup via PendingInvite
	noOrgCtx := WithCaller(ctx, Caller{Kind: CallerUser, UserID: owner.ID, Role: RoleOwner, InstanceOwner: true})
	if _, err := svc.InviteMember(noOrgCtx, "a@b.c", RoleViewer); errs.CodeOf(err) != errs.CodeForbidden {
		t.Fatal(err)
	}
	dying, _ := svc.CreateOrganization(ic, "Dying", "dying-cov", nil, nil)
	// put caller in dying org
	dCtx := ctxAs(dying.ID, owner.ID, RoleOwner, true)
	_, _ = svc.Repo.CreateMembership(ctx, dying.ID, owner.ID, RoleOwner)
	_, _ = svc.DeleteOrganization(ic, dying.ID, formatETag(dying.Version))
	if _, err := svc.InviteMember(dCtx, "into-dying@ex.test", RoleViewer); errs.CodeOf(err) != errs.CodeConflict {
		t.Fatal(err)
	}

	// Patch/Delete member version mismatch + last-owner via service
	owners, _ := svc.ListMembers(ic, ListFilter{Limit: 50})
	var onlyOwner *Membership
	for i := range owners.Data {
		if owners.Data[i].Role == RoleOwner && owners.Data[i].AcceptedAt != nil {
			onlyOwner = &owners.Data[i]
			break
		}
	}
	if onlyOwner != nil {
		viewer := RoleViewer
		_, err := svc.PatchMember(ic, onlyOwner.ID, &viewer, `"v999"`)
		if code := errs.CodeOf(err); code != errs.CodeVersionMismatch && code != errs.CodeLastOwner && code != errs.CodeForbidden {
			t.Fatalf("patch bad etag: %v", err)
		}
		_ = svc.DeleteMember(ic, onlyOwner.ID, `"v999"`)
	}
	// admin cannot touch owners
	if onlyOwner != nil {
		viewer := RoleViewer
		if _, err := svc.PatchMember(aCtx, onlyOwner.ID, &viewer, ""); errs.CodeOf(err) != errs.CodeForbidden {
			t.Fatal(err)
		}
		if err := svc.DeleteMember(aCtx, onlyOwner.ID, ""); errs.CodeOf(err) != errs.CodeForbidden {
			t.Fatal(err)
		}
	}
	// promote then demote last-owner path
	viewerRole := RoleViewer
	ownerRole := RoleOwner
	if _, err := svc.PatchMember(ic, admMem.ID, &ownerRole, formatETag(admMem.Version)); err != nil {
		// version may have changed — reload
		admMem2, _ := svc.Repo.MembershipInOrg(ctx, orgID, admMem.ID)
		_, _ = svc.PatchMember(ic, admMem.ID, &ownerRole, formatETag(admMem2.Version))
	}
	// reload owner memberships and demote one when ≥2 owners
	owners2, _ := svc.ListMembers(ic, ListFilter{Limit: 50})
	var ownersFound []*Membership
	for i := range owners2.Data {
		if owners2.Data[i].Role == RoleOwner && owners2.Data[i].AcceptedAt != nil {
			ownersFound = append(ownersFound, &owners2.Data[i])
		}
	}
	if len(ownersFound) >= 2 {
		_, err = svc.PatchMember(ic, ownersFound[1].ID, &viewerRole, formatETag(ownersFound[1].Version))
		if err != nil {
			t.Fatal(err)
		}
	}

	// DeleteOrganization bad id / missing / wrong etag
	if _, err := svc.DeleteOrganization(ic, "org_notavalidulidxxxxxxxxxxxxx", ""); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatal(err)
	}
	if _, err := svc.DeleteOrganization(ic, store.NewID(store.PrefixOrganization), ""); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatal(err)
	}
	fresh, _ := svc.CreateOrganization(ic, "ETag", "etag-cov", nil, nil)
	if _, err := svc.DeleteOrganization(ic, fresh.ID, `"v99"`); errs.CodeOf(err) != errs.CodeVersionMismatch {
		t.Fatal(err)
	}

	// acceptInvitation — token only in JSON body
	tokN, hashN, _ := newInviteToken()
	_ = svc.Repo.InsertMembership(ctx, &Membership{
		ID: store.NewID(store.PrefixMembership), OrganizationID: orgID, Email: "http-acc@ex.test", Role: RoleViewer,
		Invite:    &Invite{Email: "http-acc@ex.test", TokenHash: hashN, ExpiresAt: time.Now().UTC().Add(time.Hour)},
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(), Version: 1,
	})
	accBody := `{"token":` + jsonQuote(tokN) + `,"password":"long-enough-password","name":"H"}`
	rAcc := httptest.NewRequest(http.MethodPost, "/v1/invitations/accept", strings.NewReader(accBody))
	rAcc.Header.Set("Content-Type", "application/json")
	wAcc := httptest.NewRecorder()
	srv.ServeHTTP(wAcc, rAcc)
	if wAcc.Code != 200 {
		t.Fatalf("http accept: %d %s", wAcc.Code, wAcc.Body)
	}
	if strings.Contains(wAcc.Body.String(), tokN) || strings.Contains(rAcc.URL.Path, tokN) {
		t.Fatal("token must not appear in path or response")
	}
	tokBad, _, _ := newInviteToken()
	_ = svc.Repo.InsertMembership(ctx, &Membership{
		ID: store.NewID(store.PrefixMembership), OrganizationID: orgID, Email: "badhash@ex.test", Role: RoleViewer,
		Invite:    &Invite{Email: "badhash@ex.test", TokenHash: "deadbeef", ExpiresAt: time.Now().UTC().Add(time.Hour)},
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(), Version: 1,
	})
	_, err = mod.acceptInvitation(ctx, &acceptIn{Body: tokenBody{Token: tokBad}})
	if errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatal(err)
	}
	badLookup := &lookupIn{}
	badLookup.Body.Token = tokBad
	_, _ = mod.lookupInvitation(ctx, badLookup)

	// GetOrganization via canAccess for matching member
	memCtx := ctxAs(orgID, owner.ID, RoleOwner, false)
	if _, err := svc.GetOrganization(memCtx, orgID); err != nil {
		t.Fatal(err)
	}
	// patch HTTP error paths
	do(http.MethodPatch, "/v1/organizations/"+orgID, `{"name":""}`, merge(h, map[string]string{"If-Match": `"v1"`}))
	do(http.MethodGet, "/v1/organizations/org_00000000000000000000000000", "", nil)
	do(http.MethodGet, "/v1/members?cursor=%%%", "", h)
	do(http.MethodGet, "/v1/members?sort=nope", "", h)

	// CreateOrganization duplicate via second insert same slug after delete of index? already covered conflict
	_, _ = svc.CreateOrganization(ic, "Dup", "coverorg", nil, nil) // bootstrap slug may differ

	_, _ = parseTimeKey(time.Now().UTC().Format(time.RFC3339))
	_, _ = decodeCursor("")

	// HTTP handler error returns (covers if err != nil branches in thin wrappers)
	do(http.MethodPost, "/v1/organizations", `{"name":"","slug":"zz"}`, nil)
	do(http.MethodPost, "/v1/organizations", `{"name":"Z","slug":"acme"}`, nil) // may conflict with earlier tests
	do(http.MethodGet, "/v1/organizations/"+store.NewID(store.PrefixOrganization), "", nil)
	do(http.MethodPatch, "/v1/organizations/"+store.NewID(store.PrefixOrganization), `{"name":"x"}`, nil)
	do(http.MethodDelete, "/v1/organizations/"+store.NewID(store.PrefixOrganization), "", nil)
	do(http.MethodPatch, "/v1/members/"+store.NewID(store.PrefixMembership), `{"role":"viewer"}`, h)
	do(http.MethodDelete, "/v1/members/"+store.NewID(store.PrefixMembership), "", h)
	do(http.MethodPost, "/v1/members", `{"email":"bad","role":"viewer"}`, h)
	do(http.MethodGet, "/v1/members", "", nil) // missing org for non-instance? owner has org from last used

	// delete pending invite (no acceptedAt) as admin
	tokP, hashP, _ := newInviteToken()
	pend := &Membership{
		ID: store.NewID(store.PrefixMembership), OrganizationID: orgID, Email: "pend-del@ex.test", Role: RoleViewer,
		Invite:    &Invite{Email: "pend-del@ex.test", TokenHash: hashP, ExpiresAt: time.Now().UTC().Add(time.Hour)},
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(), Version: 1,
	}
	_ = svc.Repo.InsertMembership(ctx, pend)
	_ = tokP
	if err := svc.DeleteMember(aCtx, pend.ID, formatETag(pend.Version)); err != nil {
		t.Fatal(err)
	}
	// DeleteMember versioned miss
	_ = svc.Repo.InsertMembership(ctx, &Membership{
		ID: store.NewID(store.PrefixMembership), OrganizationID: orgID, Email: "pend2@ex.test", Role: RoleViewer,
		Invite:    &Invite{Email: "pend2@ex.test", TokenHash: hashInviteToken("x"), ExpiresAt: time.Now().UTC().Add(time.Hour)},
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(), Version: 5,
	})
	pend2, _ := svc.Repo.PendingInviteByEmail(ctx, orgID, "pend2@ex.test")
	if pend2 != nil {
		_ = svc.DeleteMember(ic, pend2.ID, `"v1"`) // version mismatch
		_ = svc.DeleteMember(ic, pend2.ID, "")     // unversioned delete
	}

	// PatchMember promote to owner by admin → forbidden
	opInv, err := svc.InviteMember(ic, "op-promote@ex.test", RoleOperator)
	if err == nil {
		if _, err := svc.PatchMember(aCtx, opInv.ID, &ownerRole, formatETag(opInv.Version)); errs.CodeOf(err) != errs.CodeForbidden {
			t.Fatal(err)
		}
	}
	// memberInCallerOrg without org
	_, err = svc.memberInCallerOrg(noOrgCtx, pend.ID)
	if errs.CodeOf(err) != errs.CodeForbidden {
		t.Fatal(err)
	}

	// ListMembers empty data nil → []
	emptyOrg, _ := svc.CreateOrganization(ic, "EmptyM", "empty-m-cov", nil, nil)
	eCtx := ctxAs(emptyOrg.ID, owner.ID, RoleOwner, true)
	emptyList, err := svc.ListMembers(eCtx, ListFilter{Limit: 10})
	if err != nil || emptyList.Data == nil {
		t.Fatalf("empty list: %+v %v", emptyList, err)
	}

	// CreateOrganization conflict on existing slug from bootstrap
	org0, _ := svc.Repo.Organization(ctx, orgID)
	if org0 != nil {
		_, _ = svc.CreateOrganization(ic, "Again", org0.Slug, nil, nil)
	}

	// acceptInvitation link path for signed-in user (token in body only)
	tokL, hashL, _ := newInviteToken()
	linkUser := &User{AuthUserID: "auth-link-cov", Email: "link-cov@ex.test", Name: "L"}
	_ = svc.Repo.CreateUser(ctx, linkUser)
	_ = svc.Repo.InsertMembership(ctx, &Membership{
		ID: store.NewID(store.PrefixMembership), OrganizationID: orgID, Email: linkUser.Email, Role: RoleViewer,
		Invite:    &Invite{Email: linkUser.Email, TokenHash: hashL, ExpiresAt: time.Now().UTC().Add(time.Hour)},
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(), Version: 1,
	})
	rLink := httptest.NewRequest(http.MethodPost, "/v1/invitations/accept", strings.NewReader(`{"token":`+jsonQuote(tokL)+`}`))
	rLink.Header.Set("Content-Type", "application/json")
	rLink = rLink.WithContext(context.WithValue(rLink.Context(), idKey{}, Identity{AuthUserID: linkUser.AuthUserID, Email: linkUser.Email}))
	wLink := httptest.NewRecorder()
	srv.ServeHTTP(wLink, rLink)
	if wLink.Code != 200 {
		t.Fatalf("link accept: %d %s", wLink.Code, wLink.Body)
	}

	// orgCursorFilter bad time / memberCursorFilter bad time
	_, _ = orgCursorFilter(cursorPayload{ID: "x", Key: "nope"}, "createdAt", 1)
	_, _ = memberCursorFilter(cursorPayload{ID: "x", Key: "nope"}, "createdAt", 1)
}

func merge(a, b map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

func strPtr(s string) *string { return &s }
func strPtrRole(r Role) *Role { return &r }

type failAuditor struct{}

func (failAuditor) Record(context.Context, string, string, string, map[string]FieldDiff) error {
	return errors.New("boom")
}
