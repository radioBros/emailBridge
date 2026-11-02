//go:build integration

package tenancy

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/store"
)

type memAuditor struct {
	mu   sync.Mutex
	recs []string
}

func (m *memAuditor) Record(_ context.Context, action, _, _ string, _ map[string]FieldDiff) error {
	m.mu.Lock()
	m.recs = append(m.recs, action)
	m.mu.Unlock()
	return nil
}

func stubJobs() JobCreator {
	return func(_ context.Context, _ string, _ any) (string, error) {
		return store.NewID(store.PrefixJob), nil
	}
}

func orgHarness(t *testing.T) (*Service, *Repo, *store.DB, *memAuditor) {
	t.Helper()
	db := testDB(t)
	aud := &memAuditor{}
	svc := &Service{
		Repo: &Repo{DB: db}, Audit: aud, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		PublicURL: "https://eb.test", Jobs: stubJobs(),
	}
	return svc, svc.Repo, db, aud
}

func ctxAs(orgID, userID string, role Role, instanceOwner bool) context.Context {
	return WithCaller(context.Background(), Caller{
		Kind: CallerUser, UserID: userID, Email: userID + "@test", OrganizationID: orgID,
		Role: role, InstanceOwner: instanceOwner,
	})
}

func TestIntegrationOrganizationsCRUD(t *testing.T) {
	svc, repo, _, aud := orgHarness(t)
	ctx := context.Background()
	owner := &User{AuthUserID: "a1", Email: "owner@t.test", Name: "O", InstanceRole: "owner"}
	if err := repo.CreateUser(ctx, owner); err != nil {
		t.Fatal(err)
	}
	ic := ctxAs("", owner.ID, RoleViewer, true)

	_, err := svc.CreateOrganization(ctx, "X", "x", nil, nil)
	if errs.CodeOf(err) != errs.CodeUnauthenticated {
		t.Fatalf("anon create: %v", err)
	}
	o, err := svc.CreateOrganization(ic, "Acme", "acme", map[string]any{"brandName": "A"}, nil)
	if err != nil || o.Slug != "acme" {
		t.Fatalf("create: %+v %v", o, err)
	}
	if _, err := svc.CreateOrganization(ic, "Acme2", "acme", nil, nil); errs.CodeOf(err) != errs.CodeConflict {
		t.Fatalf("dup slug: %v", err)
	}
	list, err := svc.ListOrganizations(ic, ListFilter{Limit: 10, Sort: "name"})
	if err != nil || len(list.Data) != 1 {
		t.Fatalf("list: %+v %v", list, err)
	}
	got, err := svc.GetOrganization(ic, o.ID)
	if err != nil || got.Name != "Acme" {
		t.Fatalf("get: %+v %v", got, err)
	}
	name := "Acme Inc"
	st := "suspended"
	patched, err := svc.PatchOrganization(ic, o.ID, OrgPatch{Name: &name, Status: &st}, formatETag(o.Version))
	if err != nil || patched.Name != name || patched.Status != st {
		t.Fatalf("patch: %+v %v", patched, err)
	}
	if _, err := svc.PatchOrganization(ic, o.ID, OrgPatch{Name: &name}, `"v1"`); errs.CodeOf(err) != errs.CodeVersionMismatch {
		t.Fatalf("etag: %v", err)
	}
	job, err := svc.DeleteOrganization(ic, o.ID, formatETag(patched.Version))
	if err != nil || job.Kind != "organization.erasure" || job.State != "queued" || job.ID == "" {
		t.Fatalf("delete: %+v %v", job, err)
	}
	svc.Jobs = nil
	o2, _ := svc.CreateOrganization(ic, "NoJobs", "nojobs", nil, nil)
	if _, err := svc.DeleteOrganization(ic, o2.ID, formatETag(o2.Version)); errs.CodeOf(err) != errs.CodeFeatureDisabled {
		t.Fatalf("no jobs: %v", err)
	}
	svc.Jobs = stubJobs()
	after, _ := repo.Organization(ctx, o.ID)
	if after.Status != "deleting" {
		t.Fatalf("status: %s", after.Status)
	}
	aud.mu.Lock()
	defer aud.mu.Unlock()
	joined := strings.Join(aud.recs, ",")
	for _, want := range []string{"organization.created", "organization.updated", "organization.deleted"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("audit missing %s in %s", want, joined)
		}
	}
}

func TestIntegrationMembersInvitePatchDeleteAndIsolation(t *testing.T) {
	svc, repo, db, aud := orgHarness(t)
	ctx := context.Background()
	orgA, _ := repo.CreateOrganization(ctx, "A", "orga")
	orgB, _ := repo.CreateOrganization(ctx, "B", "orgb")
	ownerA := &User{AuthUserID: "oa", Email: "oa@t.test", Name: "OA"}
	adminA := &User{AuthUserID: "aa", Email: "aa@t.test", Name: "AA"}
	ownerB := &User{AuthUserID: "ob", Email: "ob@t.test", Name: "OB"}
	for _, u := range []*User{ownerA, adminA, ownerB} {
		if err := repo.CreateUser(ctx, u); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := repo.CreateMembership(ctx, orgA.ID, ownerA.ID, RoleOwner); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateMembership(ctx, orgA.ID, adminA.ID, RoleAdmin); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateMembership(ctx, orgB.ID, ownerB.ID, RoleOwner); err != nil {
		t.Fatal(err)
	}

	svc.Mail = nil // no sysmail in unit harness — invite still created; mail absence is logged
	aOwner := ctxAs(orgA.ID, ownerA.ID, RoleOwner, false)
	aAdmin := ctxAs(orgA.ID, adminA.ID, RoleAdmin, false)
	bOwner := ctxAs(orgB.ID, ownerB.ID, RoleOwner, false)

	inv, err := svc.InviteMember(aOwner, "new@t.test", RoleOperator)
	if err != nil || inv.Email != "new@t.test" || inv.Invite != nil && inv.Invite.TokenHash == "" {
		// Invite is json:"-" so after insert it's still on struct
		t.Fatalf("invite: %+v %v", inv, err)
	}
	if inv.Invite == nil || inv.Invite.TokenHash == "" {
		t.Fatal("token hash missing on stored invite")
	}

	if _, err := svc.InviteMember(aAdmin, "boss@t.test", RoleOwner); errs.CodeOf(err) != errs.CodeForbidden {
		t.Fatalf("admin invite owner: %v", err)
	}
	if _, err := svc.InviteMember(aOwner, "new@t.test", RoleViewer); errs.CodeOf(err) != errs.CodeConflict {
		t.Fatalf("dup invite: %v", err)
	}

	listA, err := svc.ListMembers(aOwner, ListFilter{Limit: 50, Sort: "-createdAt"})
	if err != nil || len(listA.Data) < 3 {
		t.Fatalf("list A: %d %v", len(listA.Data), err)
	}
	listB, err := svc.ListMembers(bOwner, ListFilter{Limit: 50})
	if err != nil || len(listB.Data) != 1 {
		t.Fatalf("list B isolation: %d %v", len(listB.Data), err)
	}
	for _, m := range listB.Data {
		if m.OrganizationID != orgB.ID {
			t.Fatalf("org B saw foreign member %s", m.OrganizationID)
		}
	}
	// org A must not see org B's membership ids
	for _, m := range listA.Data {
		if m.OrganizationID != orgA.ID {
			t.Fatal("tenant leak")
		}
	}

	var ownerMem, adminMem *Membership
	for i := range listA.Data {
		m := &listA.Data[i]
		if m.UserID == ownerA.ID {
			ownerMem = m
		}
		if m.UserID == adminA.ID {
			adminMem = m
		}
	}
	viewer := RoleViewer
	if _, err := svc.PatchMember(aAdmin, ownerMem.ID, &viewer, formatETag(ownerMem.Version)); errs.CodeOf(err) != errs.CodeForbidden {
		t.Fatalf("admin demote owner: %v", err)
	}
	if err := svc.DeleteMember(aAdmin, ownerMem.ID, formatETag(ownerMem.Version)); errs.CodeOf(err) != errs.CodeForbidden {
		t.Fatalf("admin remove owner: %v", err)
	}
	// last owner
	if err := svc.DeleteMember(aOwner, ownerMem.ID, formatETag(ownerMem.Version)); errs.CodeOf(err) != errs.CodeLastOwner {
		t.Fatalf("last owner: %v", err)
	}
	// promote admin to owner, then demote original
	ownerRole := RoleOwner
	promoted, err := svc.PatchMember(aOwner, adminMem.ID, &ownerRole, formatETag(adminMem.Version))
	if err != nil {
		t.Fatal(err)
	}
	demoted, err := svc.PatchMember(aOwner, ownerMem.ID, &viewer, formatETag(ownerMem.Version))
	if err != nil || demoted.Role != RoleViewer {
		t.Fatalf("demote: %+v %v", demoted, err)
	}
	if err := svc.DeleteMember(aOwner, demoted.ID, formatETag(demoted.Version)); err != nil {
		t.Fatal(err)
	}
	_ = promoted
	_ = db
	aud.mu.Lock()
	if !strings.Contains(strings.Join(aud.recs, ","), "member.invited") {
		t.Fatal(aud.recs)
	}
	aud.mu.Unlock()
}

func TestIntegrationAcceptInviteNewAndLinkAndExpired(t *testing.T) {
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
	created := 0
	mod := &Module{
		PublicURL: "https://eb.test", Jobs: stubJobs(),
		Creator: func(_ context.Context, email, _ string) (string, error) {
			created++
			return "auth-" + email, nil
		},
		Identity: func(ctx context.Context) (Identity, bool) {
			v, ok := ctx.Value(idKey{}).(Identity)
			return v, ok
		},
	}
	if err := mod.Start(ctx, a); err != nil {
		t.Fatal(err)
	}
	org, _ := mod.service.Repo.CreateOrganization(ctx, "Inv", "inv")
	owner := &User{AuthUserID: "auth-o", Email: "o@t.test", Name: "Owner Name"}
	_ = mod.service.Repo.CreateUser(ctx, owner)
	_, _ = mod.service.Repo.CreateMembership(ctx, org.ID, owner.ID, RoleOwner)

	token, hash, err := newInviteToken()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	mem := &Membership{
		ID: store.NewID(store.PrefixMembership), OrganizationID: org.ID, Email: "invitee@t.test", Role: RoleViewer,
		InvitedBy: owner.ID, Invite: &Invite{Email: "invitee@t.test", TokenHash: hash, ExpiresAt: now.Add(time.Hour)},
		CreatedAt: now, UpdatedAt: now, Version: 1,
	}
	if err := mod.service.Repo.InsertMembership(ctx, mem); err != nil {
		t.Fatal(err)
	}

	postJSON := func(path, body string) (int, map[string]any, string) {
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out, w.Body.String()
	}

	// lookup — token only in body; response must not echo token; access path has no token
	code, look, raw := postJSON("/v1/invitations/lookup", `{"token":`+jsonQuote(token)+`}`)
	if code != 200 || look["email"] != "invitee@t.test" || look["organizationName"] != "Inv" || look["inviterName"] != "Owner Name" {
		t.Fatalf("lookup: %d %v", code, look)
	}
	if look["existingUser"] != false {
		t.Fatalf("existingUser: %v", look["existingUser"])
	}
	if strings.Contains(raw, token) {
		t.Fatal("response must not contain token")
	}

	body := `{"token":` + jsonQuote(token) + `,"password":"correct horse battery staple","name":"Invitee"}`
	code, out, raw := postJSON("/v1/invitations/accept", body)
	if code != 200 {
		t.Fatalf("accept new: %d %s", code, raw)
	}
	if created != 1 {
		t.Fatalf("created=%d", created)
	}
	if out["userId"] == nil || out["membershipId"] != mem.ID {
		t.Fatalf("body: %v", out)
	}
	if strings.Contains(raw, token) {
		t.Fatal("accept response must not contain token")
	}
	// path must not include token (route shape)
	if strings.Contains("/v1/invitations/accept", token) {
		t.Fatal("impossible")
	}

	// single use
	code, _, raw = postJSON("/v1/invitations/accept", body)
	if code != 404 && code != 409 {
		t.Fatalf("reuse: %d %s", code, raw)
	}

	// expired
	tok2, hash2, _ := newInviteToken()
	mem2 := &Membership{
		ID: store.NewID(store.PrefixMembership), OrganizationID: org.ID, Email: "late@t.test", Role: RoleViewer,
		Invite:    &Invite{Email: "late@t.test", TokenHash: hash2, ExpiresAt: now.Add(-time.Hour)},
		CreatedAt: now, UpdatedAt: now, Version: 1,
	}
	_ = mod.service.Repo.InsertMembership(ctx, mem2)
	code, _, raw = postJSON("/v1/invitations/accept", `{"token":`+jsonQuote(tok2)+`,"password":"correct horse battery staple"}`)
	if code != http.StatusGone || !strings.Contains(raw, `"code":"invitation_expired"`) {
		t.Fatalf("expired: %d %s", code, raw)
	}
	code, _, raw = postJSON("/v1/invitations/lookup", `{"token":`+jsonQuote(tok2)+`}`)
	if code != http.StatusGone || !strings.Contains(raw, `"code":"invitation_expired"`) {
		t.Fatalf("lookup expired: %d %s", code, raw)
	}

	// link existing with mismatched email
	tok3, hash3, _ := newInviteToken()
	mem3 := &Membership{
		ID: store.NewID(store.PrefixMembership), OrganizationID: org.ID, Email: "other@t.test", Role: RoleViewer,
		Invite:    &Invite{Email: "other@t.test", TokenHash: hash3, ExpiresAt: now.Add(time.Hour)},
		CreatedAt: now, UpdatedAt: now, Version: 1,
	}
	_ = mod.service.Repo.InsertMembership(ctx, mem3)
	existing := &User{AuthUserID: "auth-ex", Email: "existing@t.test", Name: "E"}
	_ = mod.service.Repo.CreateUser(ctx, existing)
	outLink, err := mod.acceptInvitation(WithCaller(ctx, Caller{
		Kind: CallerUser, UserID: existing.ID, Email: existing.Email, AuthUserID: existing.AuthUserID,
	}), &acceptIn{Body: tokenBody{Token: tok3}})
	if errs.CodeOf(err) != errs.CodeForbidden {
		t.Fatalf("email mismatch: %v %+v", err, outLink)
	}
	_, _ = db.Collection(UsersCollection).UpdateOne(ctx, map[string]any{"_id": existing.ID}, map[string]any{"$set": map[string]any{"email": "other@t.test"}})
	existing.Email = "other@t.test"
	okOut, err := mod.acceptInvitation(WithCaller(ctx, Caller{
		Kind: CallerUser, UserID: existing.ID, Email: existing.Email, AuthUserID: existing.AuthUserID,
	}), &acceptIn{Body: tokenBody{Token: tok3}})
	if err != nil || okOut.Body.UserID != existing.ID {
		t.Fatalf("link ok: %+v %v", okOut, err)
	}

	// lookup existingUser true for a fresh invite to an existing email
	tok4, hash4, _ := newInviteToken()
	_ = mod.service.Repo.InsertMembership(ctx, &Membership{
		ID: store.NewID(store.PrefixMembership), OrganizationID: org.ID, Email: existing.Email, Role: RoleViewer,
		InvitedBy: owner.ID, Invite: &Invite{Email: existing.Email, TokenHash: hash4, ExpiresAt: now.Add(time.Hour)},
		CreatedAt: now, UpdatedAt: now, Version: 1,
	})
	code, look, _ = postJSON("/v1/invitations/lookup", `{"token":`+jsonQuote(tok4)+`}`)
	if code != 200 || look["existingUser"] != true {
		t.Fatalf("lookup existing: %d %v", code, look)
	}
}

func jsonQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestIntegrationOrgMembersHTTPRoutes(t *testing.T) {
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
	ownerAuth := "auth-owner@ex.test"
	mod := &Module{
		Jobs:    stubJobs(),
		Creator: func(_ context.Context, email, _ string) (string, error) { return "auth-" + email, nil },
		Identity: func(ctx context.Context) (Identity, bool) {
			v, ok := ctx.Value(idKey{}).(Identity)
			return v, ok
		},
		Keys: func(r *http.Request) (*Caller, error) {
			if r.Header.Get("Authorization") == "Bearer ebk_members" {
				return &Caller{Kind: CallerAPIKey, APIKeyID: "key_m", OrganizationID: r.Header.Get("X-Test-Org"), Scopes: []string{"members:write"}}, nil
			}
			if r.Header.Get("Authorization") == "Bearer ebk_none" {
				return &Caller{Kind: CallerAPIKey, APIKeyID: "key_n", OrganizationID: r.Header.Get("X-Test-Org"), Scopes: []string{"logs:read"}}, nil
			}
			return nil, nil
		},
	}
	mod.Bootstrap.Email, mod.Bootstrap.Password, mod.Bootstrap.OrgName = "owner@ex.test", "pw-123456789012", "Root"
	if err := mod.Start(ctx, a); err != nil {
		t.Fatal(err)
	}
	owner, err := mod.service.Repo.UserByAuthID(ctx, "auth-"+mod.Bootstrap.Email)
	if err != nil {
		// bootstrap uses Creator which returns auth-+email
		owner, err = mod.service.Repo.UserByAuthID(ctx, ownerAuth)
		if err != nil {
			owner, err = mod.service.Repo.UserByEmail(ctx, "owner@ex.test")
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	ms, _ := mod.service.Repo.MembershipsOf(ctx, owner.ID)
	if len(ms) == 0 {
		t.Fatal("no membership")
	}
	orgID := ms[0].OrganizationID

	call := func(method, path, body string, hdr map[string]string) (int, map[string]any) {
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
		r = r.WithContext(context.WithValue(r.Context(), idKey{}, Identity{AuthUserID: owner.AuthUserID, Email: owner.Email, MFA: true}))
		w := httptest.NewRecorder()
		srv.ServeHTTP(w, r)
		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}
	code, body := call(http.MethodGet, "/v1/organizations", "", nil)
	if code != 200 {
		t.Fatalf("list orgs: %d %v", code, body)
	}
	code, body = call(http.MethodPost, "/v1/organizations", `{"name":"Two","slug":"two"}`, nil)
	if code != 201 {
		t.Fatalf("create org: %d %v", code, body)
	}
	code, body = call(http.MethodGet, "/v1/members", "", map[string]string{"EB-Organization": orgID})
	if code != 200 {
		t.Fatalf("list members: %d %v", code, body)
	}
	code, body = call(http.MethodPost, "/v1/members", `{"email":"x@y.test","role":"viewer"}`, map[string]string{"EB-Organization": orgID})
	if code != 201 {
		t.Fatalf("invite: %d %v", code, body)
	}
	// API key with members:write
	r := httptest.NewRequest(http.MethodGet, "/v1/members", nil)
	r.Header.Set("Authorization", "Bearer ebk_members")
	r.Header.Set("X-Test-Org", orgID)
	r.Header.Set("EB-Organization", orgID)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("key list members: %d %s", w.Code, w.Body)
	}
	r = httptest.NewRequest(http.MethodPost, "/v1/members", strings.NewReader(`{"email":"k@y.test","role":"viewer"}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer ebk_none")
	r.Header.Set("X-Test-Org", orgID)
	r.Header.Set("EB-Organization", orgID)
	w = httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("key without scope: %d %s", w.Code, w.Body)
	}
}
