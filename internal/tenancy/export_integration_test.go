//go:build integration

package tenancy

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
)

type exportAuditor struct {
	mu      sync.Mutex
	actions []string
}

func (a *exportAuditor) Record(_ context.Context, action, _, _ string, _ map[string]FieldDiff) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.actions = append(a.actions, action)
	return nil
}

func TestIntegrationMeExport(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	repo := &Repo{DB: db}

	org := store.NewID(store.PrefixOrganization)
	other := store.NewID(store.PrefixOrganization)
	_, _ = db.Collection(OrganizationsCollection).InsertOne(ctx, Organization{
		ID: org, Slug: "exp-org", Name: "Export Org", Status: "active", CreatedAt: now, UpdatedAt: now, Version: 1,
	})
	_, _ = db.Collection(OrganizationsCollection).InsertOne(ctx, Organization{
		ID: other, Slug: "other-org", Name: "Other", Status: "active", CreatedAt: now, UpdatedAt: now, Version: 1,
	})

	uid := store.NewID(store.PrefixUser)
	authID := "auth-" + uid
	u := &User{
		ID: uid, AuthUserID: authID, Email: "export@example.test", Name: "Exporter", Locale: "en",
		Status: "active", CreatedAt: now, UpdatedAt: now, Version: 1,
	}
	if err := repo.CreateUser(ctx, u); err != nil {
		t.Fatal(err)
	}
	_, _ = db.Collection(MembershipsCollection).InsertOne(ctx, Membership{
		ID: store.NewID(store.PrefixMembership), OrganizationID: org, UserID: uid, Role: RoleOwner,
		AcceptedAt: &now, CreatedAt: now, UpdatedAt: now, Version: 1,
	})
	_, _ = db.Collection("userSettings").InsertOne(ctx, bson.M{
		"_id": uid, "settings": bson.M{"tables": bson.M{"accounts": bson.M{"order": []string{"email"}}}},
		"updatedAt": now, "version": int64(1),
	})
	_, _ = db.Collection("auditLog").InsertOne(ctx, bson.M{
		"_id": store.NewID(store.PrefixAudit), "organizationId": org, "at": now,
		"actor":  bson.M{"type": "user", "id": uid, "ip": "9.9.9.9", "userAgent": "ua"},
		"action": "user.update", "target": bson.M{"type": "user", "id": uid},
		"changes": bson.M{
			"name":     bson.M{"after": "Exporter"},
			"password": bson.M{"after": "should-not-leak"},
		},
	})
	// someone else's audit — must not appear
	_, _ = db.Collection("auditLog").InsertOne(ctx, bson.M{
		"_id": store.NewID(store.PrefixAudit), "organizationId": org, "at": now,
		"actor":  bson.M{"type": "user", "id": store.NewID(store.PrefixUser)},
		"action": "member.invited", "target": bson.M{"type": "membership", "id": "mem_x"},
	})

	aud := &exportAuditor{}
	deps := MeExportDeps{
		DB: db, Repo: repo, Audit: aud,
		Sessions: func(_ context.Context, authUserID, _ string) ([]ExportSession, error) {
			if authUserID != authID {
				t.Fatalf("auth id: %s", authUserID)
			}
			return []ExportSession{{
				ID: "sess_1", Current: true, Method: "password",
				CreatedAt: now, LastActiveAt: now, ExpiresAt: now.Add(time.Hour),
				IP: "1.2.3.4", UserAgent: "test",
			}}, nil
		},
		Passkeys: func(_ context.Context, authUserID string) ([]ExportPasskey, error) {
			return []ExportPasskey{{ID: "pk_1", Name: "YubiKey"}}, nil
		},
	}

	// API key forbidden
	keyCtx := WithCaller(ctx, Caller{Kind: CallerAPIKey, APIKeyID: "key_x", OrganizationID: org})
	if _, err := deps.Export(keyCtx); errs.CodeOf(err) != errs.CodeForbidden {
		t.Fatalf("api key: %v", err)
	}

	userCtx := WithCaller(ctx, Caller{Kind: CallerUser, UserID: uid, AuthUserID: authID, OrganizationID: org, Role: RoleOwner})
	out, err := deps.Export(userCtx)
	if err != nil {
		t.Fatal(err)
	}
	if out.Profile == nil || out.Profile.Email != "export@example.test" {
		t.Fatalf("profile: %+v", out.Profile)
	}
	raw, _ := json.Marshal(out)
	s := string(raw)
	if strings.Contains(s, "authUserId") || strings.Contains(s, "should-not-leak") ||
		strings.Contains(s, "passwordHash") || strings.Contains(s, "totpSecret") ||
		strings.Contains(s, `"token"`) {
		t.Fatalf("secrets in export JSON: %s", s)
	}
	if len(out.Memberships) != 1 || out.Memberships[0].OrganizationName != "Export Org" {
		t.Fatalf("memberships: %+v", out.Memberships)
	}
	if out.Settings["tables"] == nil {
		t.Fatalf("settings: %+v", out.Settings)
	}
	if len(out.Sessions) != 1 || out.Sessions[0].ID != "sess_1" {
		t.Fatalf("sessions: %+v", out.Sessions)
	}
	if len(out.Passkeys) != 1 || out.Passkeys[0].Name != "YubiKey" {
		t.Fatalf("passkeys: %+v", out.Passkeys)
	}
	if out.Audit.Limit != MeExportAuditLimit || len(out.Audit.Data) != 1 {
		t.Fatalf("audit: limit=%d n=%d", out.Audit.Limit, len(out.Audit.Data))
	}
	if out.Audit.Data[0].Actor["ip"] != nil {
		t.Fatalf("audit ip leaked: %+v", out.Audit.Data[0].Actor)
	}
	pw := out.Audit.Data[0].Changes["password"].(map[string]any)
	if pw["after"] != "[redacted]" {
		t.Fatalf("password change not redacted: %+v", pw)
	}

	aud.mu.Lock()
	found := false
	for _, a := range aud.actions {
		if a == "me.export" {
			found = true
		}
	}
	aud.mu.Unlock()
	if !found {
		t.Fatal("me.export audit missing")
	}

	// handle wrapper + session/passkey error paths
	if _, err := deps.handle(userCtx, &meExportIn{}); err != nil {
		t.Fatal(err)
	}
	deps.Sessions = func(context.Context, string, string) ([]ExportSession, error) {
		return nil, errors.New("sessions down")
	}
	if _, err := deps.Export(userCtx); errs.CodeOf(err) != errs.CodeInternalError {
		t.Fatalf("sessions err: %v", err)
	}
	deps.Sessions = nil
	deps.Passkeys = func(context.Context, string) ([]ExportPasskey, error) {
		return nil, errors.New("passkeys down")
	}
	if _, err := deps.Export(userCtx); errs.CodeOf(err) != errs.CodeInternalError {
		t.Fatalf("passkeys err: %v", err)
	}
	deps.Passkeys = nil
	missing := WithCaller(ctx, Caller{Kind: CallerUser, UserID: store.NewID(store.PrefixUser), AuthUserID: "nope"})
	if _, err := deps.Export(missing); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatalf("missing user: %v", err)
	}
	// closed DB: the audit Find error fails the export (never an empty trail).
	_ = db.Close(ctx)
	rows, next, err := deps.loadAudit(ctx, uid, nil, time.Now().UTC())
	if errs.CodeOf(err) != errs.CodeInternalError || rows != nil || next != nil {
		t.Fatalf("closed loadAudit: %d %v %v", len(rows), next, err)
	}
}

// TestIntegrationMeExportAuditPaging: the audit trail pages by a keyset cursor (ties on `at` included); only
// the first page counts against the rate limit and writes me.export; a bad cursor is 422.
func TestIntegrationMeExportAuditPaging(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	repo := &Repo{DB: db}
	uid := store.NewID(store.PrefixUser)
	if err := repo.CreateUser(ctx, &User{ID: uid, Email: "pager@example.test", Status: "active", CreatedAt: now, UpdatedAt: now, Version: 1}); err != nil {
		t.Fatal(err)
	}
	const total = MeExportAuditLimit + 51
	docs := make([]any, 0, total)
	for i := range total {
		docs = append(docs, bson.M{
			"_id": store.NewID(store.PrefixAudit), "at": now.Add(-time.Duration(i/3) * time.Second), // groups of 3 share `at`
			"actor": bson.M{"type": "user", "id": uid}, "action": "user.update", "target": bson.M{"type": "user", "id": uid},
		})
	}
	if _, err := db.Collection("auditLog").InsertMany(ctx, docs); err != nil {
		t.Fatal(err)
	}
	rateCalls := 0
	aud := &exportAuditor{}
	deps := MeExportDeps{
		DB: db, Repo: repo, Audit: aud, CursorKey: []byte("test-me-export-cursor-key"),
		Rate: func(context.Context) error { rateCalls++; return nil },
	}
	userCtx := WithCaller(ctx, Caller{Kind: CallerUser, UserID: uid})

	first, err := deps.ExportPage(userCtx, "")
	if err != nil || len(first.Audit.Data) != MeExportAuditLimit || first.Audit.NextCursor == nil {
		t.Fatalf("first page: %v n=%d next=%v", err, len(first.Audit.Data), first.Audit.NextCursor)
	}
	out, err := deps.handle(userCtx, &meExportIn{AuditCursor: *first.Audit.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	second := out.Body
	if len(second.Audit.Data) != total-MeExportAuditLimit || second.Audit.NextCursor != nil {
		t.Fatalf("second page: n=%d next=%v", len(second.Audit.Data), second.Audit.NextCursor)
	}
	seen := map[string]bool{}
	for _, r := range append(first.Audit.Data, second.Audit.Data...) {
		if seen[r.ID] {
			t.Fatalf("%s on two pages", r.ID)
		}
		seen[r.ID] = true
	}
	if len(seen) != total {
		t.Fatalf("paged %d of %d", len(seen), total)
	}
	aud.mu.Lock()
	exports := 0
	for _, a := range aud.actions {
		if a == "me.export" {
			exports++
		}
	}
	aud.mu.Unlock()
	if rateCalls != 1 || exports != 1 {
		t.Fatalf("follow-up page counted: rate=%d audit=%d", rateCalls, exports)
	}
	for _, bad := range []string{"%%%", "e30", "eyJpZCI6IngifQ"} { // not base64, {}, {"id":"x"} without at
		if _, err := deps.ExportPage(userCtx, bad); errs.CodeOf(err) != errs.CodeValidationFailed {
			t.Fatalf("cursor %q: %v", bad, err)
		}
	}
}
