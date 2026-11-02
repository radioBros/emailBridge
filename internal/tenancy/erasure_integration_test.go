//go:build integration

package tenancy

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
)

type eraseAuditor struct {
	mu   sync.Mutex
	recs []struct {
		Action string
		Counts map[string]int64
	}
}

func (a *eraseAuditor) Record(_ context.Context, action, _, _ string, changes map[string]FieldDiff) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	rec := struct {
		Action string
		Counts map[string]int64
	}{Action: action}
	if c, ok := changes["counts"]; ok {
		if m, ok := c.After.(map[string]int64); ok {
			rec.Counts = m
		}
	}
	a.recs = append(a.recs, rec)
	return nil
}

func seedOrgErasure(t *testing.T, db *store.DB, orgID string) (accountIDs []string, jobID string) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	_, err := db.Collection(OrganizationsCollection).InsertOne(ctx, Organization{
		ID: orgID, Slug: "erase-" + orgID[len(orgID)-6:], Name: "Erase Me", Status: "deleting",
		CreatedAt: now, UpdatedAt: now, Version: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	uid := store.NewID(store.PrefixUser)
	_, _ = db.Collection(UsersCollection).InsertOne(ctx, User{
		ID: uid, AuthUserID: "auth-" + uid, Email: "member@example.test", Name: "Mem Ber",
		Status: "active", CreatedAt: now, UpdatedAt: now, Version: 1,
	})
	_, _ = db.Collection(MembershipsCollection).InsertOne(ctx, Membership{
		ID: store.NewID(store.PrefixMembership), OrganizationID: orgID, UserID: uid, Role: RoleOwner,
		CreatedAt: now, UpdatedAt: now, Version: 1,
	})
	_, _ = db.Collection(MembershipsCollection).InsertOne(ctx, Membership{
		ID: store.NewID(store.PrefixMembership), OrganizationID: orgID, Email: "invitee@example.test", Role: RoleViewer,
		Invite:    &Invite{Email: "invitee@example.test", TokenHash: "hash", ExpiresAt: now.Add(time.Hour)},
		CreatedAt: now, UpdatedAt: now, Version: 1,
	})
	_, _ = db.Collection("settings").InsertOne(ctx, bson.M{"_id": "org:" + orgID, "values": bson.M{"x": 1}})
	for i := 0; i < 2; i++ {
		acc := store.NewID(store.PrefixAccount)
		accountIDs = append(accountIDs, acc)
		_, _ = db.Collection("accounts").InsertOne(ctx, bson.M{
			"_id": acc, "organizationId": orgID, "email": "a" + string(rune('0'+i)) + "@ex.test", "state": "deleting",
		})
	}
	_, _ = db.Collection("webhookEndpoints").InsertOne(ctx, bson.M{"_id": "whe_1", "organizationId": orgID, "name": "hook"})
	_, _ = db.Collection("routes").InsertOne(ctx, bson.M{"_id": "rte_1", "organizationId": orgID})
	_, _ = db.Collection("routeVersions").InsertOne(ctx, bson.M{"_id": "rtv_1", "organizationId": orgID, "routeId": "rte_1"})
	_, _ = db.Collection("ruleErrors").InsertOne(ctx, bson.M{"_id": "err_1", "organizationId": orgID})
	_, _ = db.Collection("templates").InsertOne(ctx, bson.M{"_id": "tpl_1", "organizationId": orgID})
	_, _ = db.Collection("templateVersions").InsertOne(ctx, bson.M{"_id": "tplv_1", "organizationId": orgID, "templateId": "tpl_1"})
	_, _ = db.Collection("smtpGateways").InsertOne(ctx, bson.M{"_id": "gwy_1", "organizationId": orgID})
	_, _ = db.Collection("suppressionLists").InsertOne(ctx, bson.M{"_id": "sup_1", "organizationId": orgID})
	_, _ = db.Collection("suppressionEntries").InsertOne(ctx, bson.M{"_id": "sue_1", "organizationId": orgID})
	_, _ = db.Collection("integrationInstances").InsertOne(ctx, bson.M{"_id": "ini_1", "organizationId": orgID})
	_, _ = db.Collection("apiKeys").InsertOne(ctx, bson.M{"_id": store.NewID(store.PrefixAPIKey), "organizationId": orgID, "name": "k"})
	_, _ = db.Collection("aiConfigs").InsertOne(ctx, bson.M{"_id": "aic_1", "organizationId": orgID})
	_, _ = db.Collection("oauthApps").InsertOne(ctx, bson.M{"_id": "oap_1", "organizationId": orgID})
	_, _ = db.Collection("exports").InsertOne(ctx, bson.M{"_id": "exp_1", "organizationId": orgID})
	_, _ = db.Collection("connectLinks").InsertOne(ctx, bson.M{"_id": "lnk_1", "organizationId": orgID})
	_, _ = db.Collection("capturedMessages").InsertOne(ctx, bson.M{"_id": "cap_1", "organizationId": orgID})
	_, _ = db.Collection("drafts").InsertOne(ctx, bson.M{"_id": "drf_1", "organizationId": orgID})
	_, _ = db.Collection("deliveryTests").InsertOne(ctx, bson.M{"_id": "dtt_1", "organizationId": orgID})
	_, _ = db.Collection("trackingLinks").InsertOne(ctx, bson.M{"_id": "trl_1", "organizationId": orgID})
	_, _ = db.Collection("trackingEvents").InsertOne(ctx, bson.M{"_id": "tre_1", "organizationId": orgID})
	_, _ = db.Collection("dataKeys").InsertOne(ctx, bson.M{"_id": "dek_1", "organizationId": orgID, "status": "active"})
	_, _ = db.Collection("gmailSyncState").InsertOne(ctx, bson.M{"_id": "acc_g1", "organizationId": orgID})
	_, _ = db.Collection("graphSyncState").InsertOne(ctx, bson.M{"_id": "acc_g2", "organizationId": orgID})
	_, _ = db.Collection("msaCredentials").InsertOne(ctx, bson.M{"_id": "acc_m1", "organizationId": orgID})
	_, _ = db.Collection("imapProxyCredentials").InsertOne(ctx, bson.M{"_id": "acc_i1", "organizationId": orgID})
	_, _ = db.Collection("idempotencyKeys").InsertOne(ctx, bson.M{"_id": "idk_1", "organizationId": orgID})
	_, _ = db.Collection("events").InsertOne(ctx, bson.M{"_id": store.NewID(store.PrefixEvent), "organizationId": orgID})
	_, _ = db.Collection("userSettings").InsertOne(ctx, bson.M{"_id": uid, "settings": bson.M{"x": 1}, "version": int64(1)})
	jobID = store.NewID(store.PrefixJob)
	_, _ = db.Collection("jobs").InsertOne(ctx, bson.M{"_id": jobID, "organizationId": orgID, "kind": OrganizationErasureKind, "state": "running"})
	_, _ = db.Collection("jobs").InsertOne(ctx, bson.M{"_id": store.NewID(store.PrefixJob), "organizationId": orgID, "kind": "other", "state": "queued"})
	_, _ = db.Collection("auditLog").InsertOne(ctx, bson.M{
		"_id": store.NewID(store.PrefixAudit), "organizationId": orgID, "at": now,
		"actor":  bson.M{"type": "user", "id": uid, "ip": "1.2.3.4", "userAgent": "test-agent"},
		"action": "member.invited", "target": bson.M{"type": "membership", "id": "mem_x"},
		"changes": bson.M{"email": bson.M{"after": "invitee@example.test"}},
	})
	return accountIDs, jobID
}

func TestIntegrationOrganizationErasure(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	org := store.NewID(store.PrefixOrganization)
	other := store.NewID(store.PrefixOrganization)
	now := time.Now().UTC()
	keepUser := store.NewID(store.PrefixUser)
	_, _ = db.Collection(OrganizationsCollection).InsertOne(ctx, Organization{
		ID: other, Slug: "keep-me", Name: "Keep", Status: "active", CreatedAt: now, UpdatedAt: now, Version: 1,
	})
	_, _ = db.Collection(MembershipsCollection).InsertOne(ctx, Membership{
		ID: store.NewID(store.PrefixMembership), OrganizationID: other, UserID: keepUser, Role: RoleOwner,
		CreatedAt: now, UpdatedAt: now, Version: 1,
	})
	_, _ = db.Collection("settings").InsertOne(ctx, bson.M{"_id": "org:" + other, "values": bson.M{"y": 2}})
	_, _ = db.Collection("settings").InsertOne(ctx, bson.M{"_id": "global", "values": bson.M{"keep": true}})
	_, _ = db.Collection("webhookEndpoints").InsertOne(ctx, bson.M{"_id": "whe_keep", "organizationId": other})
	_, _ = db.Collection("templateVersions").InsertOne(ctx, bson.M{"_id": "tplv_keep", "organizationId": other})
	_, _ = db.Collection("aiConfigs").InsertOne(ctx, bson.M{"_id": "aic_keep", "organizationId": other})
	_, _ = db.Collection("oauthApps").InsertOne(ctx, bson.M{"_id": "oap_keep", "organizationId": other})
	_, _ = db.Collection("trackingLinks").InsertOne(ctx, bson.M{"_id": "trl_keep", "organizationId": other})
	_, _ = db.Collection("dataKeys").InsertOne(ctx, bson.M{"_id": "dek_keep", "organizationId": other})
	_, _ = db.Collection("userSettings").InsertOne(ctx, bson.M{"_id": keepUser, "settings": bson.M{"keep": true}, "version": int64(1)})

	accounts, jobID := seedOrgErasure(t, db, org)
	// Dual-membership user: also member of other — userSettings must survive.
	dual := store.NewID(store.PrefixUser)
	_, _ = db.Collection(UsersCollection).InsertOne(ctx, User{
		ID: dual, AuthUserID: "auth-" + dual, Email: "dual@example.test", Name: "Dual",
		Status: "active", CreatedAt: now, UpdatedAt: now, Version: 1,
	})
	_, _ = db.Collection(MembershipsCollection).InsertOne(ctx, Membership{
		ID: store.NewID(store.PrefixMembership), OrganizationID: org, UserID: dual, Role: RoleViewer,
		CreatedAt: now, UpdatedAt: now, Version: 1,
	})
	_, _ = db.Collection(MembershipsCollection).InsertOne(ctx, Membership{
		ID: store.NewID(store.PrefixMembership), OrganizationID: other, UserID: dual, Role: RoleViewer,
		CreatedAt: now, UpdatedAt: now, Version: 1,
	})
	_, _ = db.Collection("userSettings").InsertOne(ctx, bson.M{"_id": dual, "settings": bson.M{"dual": true}, "version": int64(1)})
	erasedAccounts := map[string]bool{}
	var revoked int64
	aud := &eraseAuditor{}

	deps := ErasureDeps{
		DB: db, Audit: aud,
		ListAccountIDs: func(_ context.Context, orgID string) ([]string, error) {
			cur, err := db.Collection("accounts").Find(ctx, bson.M{"organizationId": orgID})
			if err != nil {
				return nil, err
			}
			defer func() { _ = cur.Close(ctx) }()
			var ids []string
			for cur.Next(ctx) {
				var row bson.M
				_ = cur.Decode(&row)
				if id, ok := row["_id"].(string); ok {
					ids = append(ids, id)
				}
			}
			return ids, nil
		},
		EraseAccount: func(_ context.Context, accountID, orgID string, _ func() bool) error {
			_, err := db.Collection("accounts").DeleteOne(ctx, bson.M{"_id": accountID, "organizationId": orgID})
			erasedAccounts[accountID] = true
			return err
		},
		RevokeAPIKeys: func(_ context.Context, orgID string) (int64, error) {
			n, err := db.Collection("apiKeys").CountDocuments(ctx, bson.M{"organizationId": orgID})
			revoked = n
			return n, err
		},
	}

	var registered ErasureHandler
	RegisterErasure(func(_ string, h ErasureHandler) { registered = h }, deps)
	payload, _ := json.Marshal(map[string]string{"organizationId": org})
	var lastDone, lastTotal int64
	res, err := registered(ctx, jobID, payload, func(done, total int64) { lastDone, lastTotal = done, total }, func() bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	counts, ok := res.(map[string]int64)
	if !ok || counts["accounts"] != 2 || counts["organization"] != 1 {
		t.Fatalf("counts: %+v", counts)
	}
	if lastDone != lastTotal || lastTotal < 1 {
		t.Fatalf("progress: %d/%d", lastDone, lastTotal)
	}
	if len(erasedAccounts) != 2 || revoked != 1 {
		t.Fatalf("fakes: accounts=%d revoked=%d", len(erasedAccounts), revoked)
	}
	for _, id := range accounts {
		if !erasedAccounts[id] {
			t.Fatalf("account not erased: %s", id)
		}
	}

	assertGone := func(coll string, filter bson.M) {
		t.Helper()
		n, err := db.Collection(coll).CountDocuments(ctx, filter)
		if err != nil || n != 0 {
			t.Fatalf("%s still has %d: %v", coll, n, err)
		}
	}
	assertGone(OrganizationsCollection, bson.M{"_id": org})
	assertGone(MembershipsCollection, bson.M{"organizationId": org})
	assertGone("accounts", bson.M{"organizationId": org})
	assertGone("webhookEndpoints", bson.M{"organizationId": org})
	assertGone("routes", bson.M{"organizationId": org})
	assertGone("routeVersions", bson.M{"organizationId": org})
	assertGone("ruleErrors", bson.M{"organizationId": org})
	assertGone("templates", bson.M{"organizationId": org})
	assertGone("templateVersions", bson.M{"organizationId": org})
	assertGone("smtpGateways", bson.M{"organizationId": org})
	assertGone("suppressionLists", bson.M{"organizationId": org})
	assertGone("suppressionEntries", bson.M{"organizationId": org})
	assertGone("integrationInstances", bson.M{"organizationId": org})
	assertGone("apiKeys", bson.M{"organizationId": org})
	assertGone("aiConfigs", bson.M{"organizationId": org})
	assertGone("oauthApps", bson.M{"organizationId": org})
	assertGone("exports", bson.M{"organizationId": org})
	assertGone("connectLinks", bson.M{"organizationId": org})
	assertGone("capturedMessages", bson.M{"organizationId": org})
	assertGone("drafts", bson.M{"organizationId": org})
	assertGone("deliveryTests", bson.M{"organizationId": org})
	assertGone("trackingLinks", bson.M{"organizationId": org})
	assertGone("trackingEvents", bson.M{"organizationId": org})
	assertGone("dataKeys", bson.M{"organizationId": org})
	assertGone("gmailSyncState", bson.M{"organizationId": org})
	assertGone("graphSyncState", bson.M{"organizationId": org})
	assertGone("msaCredentials", bson.M{"organizationId": org})
	assertGone("imapProxyCredentials", bson.M{"organizationId": org})
	assertGone("idempotencyKeys", bson.M{"organizationId": org})
	assertGone("events", bson.M{"organizationId": org})
	assertGone("settings", bson.M{"_id": "org:" + org})
	// current job kept; other org jobs removed
	if n, _ := db.Collection("jobs").CountDocuments(ctx, bson.M{"organizationId": org, "_id": jobID}); n != 1 {
		t.Fatal("erasure job deleted")
	}
	if n, _ := db.Collection("jobs").CountDocuments(ctx, bson.M{"organizationId": org, "_id": bson.M{"$ne": jobID}}); n != 0 {
		t.Fatal("other jobs remain")
	}
	// global settings untouched; other org rows untouched
	if n, _ := db.Collection("settings").CountDocuments(ctx, bson.M{"_id": "global"}); n != 1 {
		t.Fatal("global settings erased")
	}
	if n, _ := db.Collection("userSettings").CountDocuments(ctx, bson.M{"_id": keepUser}); n != 1 {
		t.Fatal("other userSettings erased")
	}
	if n, _ := db.Collection("userSettings").CountDocuments(ctx, bson.M{"_id": dual}); n != 1 {
		t.Fatal("dual-member userSettings erased")
	}
	if n, _ := db.Collection("userSettings").CountDocuments(ctx, bson.M{"settings.x": 1}); n != 0 {
		t.Fatal("orphan member userSettings remain")
	}
	if n, _ := db.Collection("aiConfigs").CountDocuments(ctx, bson.M{"organizationId": other}); n != 1 {
		t.Fatal("other aiConfigs erased")
	}
	if n, _ := db.Collection("dataKeys").CountDocuments(ctx, bson.M{"organizationId": other}); n != 1 {
		t.Fatal("other dataKeys erased")
	}
	// audit kept and scrubbed
	var audit bson.M
	if err := db.Collection("auditLog").FindOne(ctx, bson.M{"organizationId": org, "action": "member.invited"}).Decode(&audit); err != nil {
		t.Fatal("audit missing", err)
	}
	actor := asMap(audit["actor"])
	if actor == nil {
		t.Fatal("audit actor missing")
	}
	if _, ok := actor["ip"]; ok {
		t.Fatalf("audit ip kept: %+v", actor)
	}
	if _, ok := actor["userAgent"]; ok {
		t.Fatalf("audit userAgent kept: %+v", actor)
	}
	emailFC := asMap(asMap(audit["changes"])["email"])
	if emailFC == nil {
		t.Fatal("audit email change missing")
	}
	if ch := emailFC["after"]; ch != "" {
		t.Fatalf("audit email kept: %v", ch)
	}
	// second org untouched
	if n, _ := db.Collection(OrganizationsCollection).CountDocuments(ctx, bson.M{"_id": other}); n != 1 {
		t.Fatal("other org erased")
	}
	if n, _ := db.Collection(MembershipsCollection).CountDocuments(ctx, bson.M{"organizationId": other}); n != 2 {
		t.Fatalf("other memberships erased: n=%d", n)
	}
	if n, _ := db.Collection("webhookEndpoints").CountDocuments(ctx, bson.M{"organizationId": other}); n != 1 {
		t.Fatal("other endpoint erased")
	}
	if n, _ := db.Collection("settings").CountDocuments(ctx, bson.M{"_id": "org:" + other}); n != 1 {
		t.Fatal("other settings erased")
	}
	aud.mu.Lock()
	found := false
	for _, r := range aud.recs {
		if r.Action == "organization.erased" {
			found = true
		}
	}
	aud.mu.Unlock()
	if !found {
		t.Fatal("organization.erased audit missing")
	}

	// idempotent re-run after success
	res2, err := registered(ctx, jobID, payload, func(int64, int64) {}, func() bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	if c2 := res2.(map[string]int64); c2["organization"] != 0 {
		t.Fatalf("re-run counts: %+v", c2)
	}
}

func TestIntegrationOrganizationErasureCrashResume(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	org := store.NewID(store.PrefixOrganization)
	_, jobID := seedOrgErasure(t, db, org)

	var steps int
	crashAfter := 3
	deps := ErasureDeps{
		DB: db,
		ListAccountIDs: func(_ context.Context, orgID string) ([]string, error) {
			cur, err := db.Collection("accounts").Find(ctx, bson.M{"organizationId": orgID})
			if err != nil {
				return nil, err
			}
			defer func() { _ = cur.Close(ctx) }()
			var ids []string
			for cur.Next(ctx) {
				var row bson.M
				_ = cur.Decode(&row)
				if id, ok := row["_id"].(string); ok {
					ids = append(ids, id)
				}
			}
			return ids, nil
		},
		EraseAccount: func(_ context.Context, accountID, orgID string, _ func() bool) error {
			_, err := db.Collection("accounts").DeleteOne(ctx, bson.M{"_id": accountID, "organizationId": orgID})
			return err
		},
		RevokeAPIKeys: func(_ context.Context, orgID string) (int64, error) {
			return db.Collection("apiKeys").CountDocuments(ctx, bson.M{"organizationId": orgID})
		},
		AfterStep: func(string) error {
			steps++
			if steps == crashAfter {
				return errors.New("simulated crash")
			}
			return nil
		},
	}
	var h ErasureHandler
	RegisterErasure(func(_ string, handler ErasureHandler) { h = handler }, deps)
	payload, _ := json.Marshal(map[string]string{"organizationId": org})
	_, err := h(ctx, jobID, payload, func(int64, int64) {}, func() bool { return false })
	if err == nil || err.Error() != "simulated crash" {
		t.Fatalf("want crash, got %v", err)
	}
	// org still present mid-run
	if n, _ := db.Collection(OrganizationsCollection).CountDocuments(ctx, bson.M{"_id": org}); n != 1 {
		t.Fatal("org gone too early")
	}

	deps.AfterStep = nil
	RegisterErasure(func(_ string, handler ErasureHandler) { h = handler }, deps)
	_, err = h(ctx, jobID, payload, func(int64, int64) {}, func() bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := db.Collection(OrganizationsCollection).CountDocuments(ctx, bson.M{"_id": org}); n != 0 {
		t.Fatal("org remains after resume")
	}
	if n, _ := db.Collection(MembershipsCollection).CountDocuments(ctx, bson.M{"organizationId": org}); n != 0 {
		t.Fatal("memberships remain after resume")
	}
	if n, _ := db.Collection("auditLog").CountDocuments(ctx, bson.M{"organizationId": org}); n < 1 {
		t.Fatal("audit should remain")
	}
}

func TestIntegrationOrganizationErasureCancelled(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	org := store.NewID(store.PrefixOrganization)
	_, jobID := seedOrgErasure(t, db, org)
	deps := ErasureDeps{
		DB: db,
		ListAccountIDs: func(context.Context, string) ([]string, error) {
			return []string{store.NewID(store.PrefixAccount)}, nil
		},
		EraseAccount: func(context.Context, string, string, func() bool) error { return nil },
	}
	var h ErasureHandler
	RegisterErasure(func(_ string, handler ErasureHandler) { h = handler }, deps)
	payload, _ := json.Marshal(map[string]string{"organizationId": org})
	_, err := h(ctx, jobID, payload, nil, func() bool { return true })
	if errs.CodeOf(err) != errs.CodeConflict {
		t.Fatalf("cancel: %v", err)
	}
}

func TestIntegrationErasureHelpersClosedDBAndEmptyJob(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	org := store.NewID(store.PrefixOrganization)
	_, _ = db.Collection("jobs").InsertOne(ctx, bson.M{"_id": store.NewID(store.PrefixJob), "organizationId": org})
	_, _ = db.Collection("auditLog").InsertOne(ctx, bson.M{
		"_id": store.NewID(store.PrefixAudit), "organizationId": org, "at": time.Now().UTC(),
		"actor": bson.M{"type": "system", "id": "sys"}, "action": "x", "target": bson.M{"type": "t", "id": "1"},
		"changes": bson.D{{Key: "note", Value: bson.D{{Key: "after", Value: "hello world"}}}},
	})
	d := ErasureDeps{DB: db}
	if n, err := d.deleteOrgJobs(ctx, org, ""); err != nil || n != 1 {
		t.Fatalf("delete all jobs: %d %v", n, err)
	}
	if n, err := d.reduceAuditPersonal(ctx, org); err != nil || n != 1 {
		t.Fatalf("scrub: %d %v", n, err)
	}
	_ = db.Close(ctx)
	if n, err := d.deleteByOrg(ctx, "webhookEndpoints", org); err != nil || n != 0 {
		t.Fatalf("closed deleteByOrg: %d %v", n, err)
	}
	if n, err := d.deleteOrgSettings(ctx, org); err != nil || n != 0 {
		t.Fatalf("closed settings: %d %v", n, err)
	}
	if n, err := d.deleteOrgJobs(ctx, org, "x"); err != nil || n != 0 {
		t.Fatalf("closed jobs: %d %v", n, err)
	}
	if n, err := d.reduceAuditPersonal(ctx, org); err != nil || n != 0 {
		t.Fatalf("closed scrub: %d %v", n, err)
	}
	// deleteOrganizationDoc surfaces the error
	if _, err := d.deleteOrganizationDoc(ctx, org); err == nil {
		t.Fatal("closed org delete should err")
	}
}

func TestIntegrationErasureAfterStepOnCollections(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	org := store.NewID(store.PrefixOrganization)
	_, jobID := seedOrgErasure(t, db, org)
	deps := ErasureDeps{
		DB:             db,
		ListAccountIDs: func(context.Context, string) ([]string, error) { return nil, nil },
		RevokeAPIKeys:  func(context.Context, string) (int64, error) { return 0, nil },
		AfterStep: func(step string) error {
			if step == "coll:routes" {
				return errors.New("boom-routes")
			}
			return nil
		},
	}
	payload, _ := json.Marshal(map[string]string{"organizationId": org})
	_, err := deps.handle(ctx, jobID, payload, nil, nil)
	if err == nil || err.Error() != "boom-routes" {
		t.Fatalf("got %v", err)
	}
}
