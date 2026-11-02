package tenancy

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
)

func TestScrubChangesKeepsIDsDropsPersonal(t *testing.T) {
	uid := store.NewID(store.PrefixUser)
	in := bson.M{
		"email":  bson.M{"before": "a@b.c", "after": "x@y.z"},
		"user":   bson.M{"after": uid},
		"name":   bson.M{"after": "Ada Lovelace"},
		"status": bson.M{"before": "active", "after": "deleting"},
		"nested": bson.D{{Key: "after", Value: "person@x.test"}},
		"skip":   "plain",
	}
	out := scrubChanges(in)
	email := out["email"].(bson.M)
	if email["before"] != "" || email["after"] != "" {
		t.Fatalf("email not scrubbed: %+v", email)
	}
	if out["user"].(bson.M)["after"] != uid {
		t.Fatalf("user id lost: %+v", out["user"])
	}
	if out["name"].(bson.M)["after"] != "" {
		t.Fatalf("name not scrubbed: %+v", out["name"])
	}
	if out["status"].(bson.M)["after"] != "deleting" {
		t.Fatalf("status scrubbed: %+v", out["status"])
	}
	if out["nested"].(bson.M)["after"] != "" {
		t.Fatalf("bson.D email not scrubbed: %+v", out["nested"])
	}
	if out["skip"] != "plain" {
		t.Fatalf("non-map kept: %+v", out["skip"])
	}
}

func TestScrubValueNestedAndArrays(t *testing.T) {
	got := scrubValue(bson.A{"a@b.c", store.NewID(store.PrefixJob), map[string]any{"x": "Hi there friend"}})
	a := got.(bson.A)
	if a[0] != "" {
		t.Fatalf("array email: %v", a[0])
	}
	if a[1] == "" {
		t.Fatal("job id scrubbed")
	}
	if a[2].(bson.M)["x"] != "" {
		t.Fatalf("nested map: %+v", a[2])
	}
	got2 := scrubValue([]any{"ok", "name with spaces"})
	s := got2.([]any)
	if s[0] != "ok" || s[1] != "" {
		t.Fatalf("[]any: %+v", s)
	}
	if scrubValue(42) != 42 {
		t.Fatal("non-string")
	}
	if asMap(nil) != nil || asMap(123) != nil {
		t.Fatal("asMap nil")
	}
	if asMap(map[string]any{"a": 1})["a"] != 1 {
		t.Fatal("asMap map")
	}
}

func TestRegisterErasureAndValidation(t *testing.T) {
	var kind string
	var h ErasureHandler
	RegisterErasure(nil, ErasureDeps{}) // no-op
	RegisterErasure(func(k string, handler ErasureHandler) {
		kind, h = k, handler
	}, ErasureDeps{})
	if kind != OrganizationErasureKind || h == nil {
		t.Fatalf("register: %q %v", kind, h)
	}
	_, err := h(context.Background(), "job_x", json.RawMessage(`{}`), nil, nil)
	if errs.CodeOf(err) != errs.CodeInternalError {
		t.Fatalf("nil db: %v", err)
	}
	_, err = ErasureDeps{DB: &store.DB{}}.handle(context.Background(), "", json.RawMessage(`{`), nil, func() bool { return false })
	if errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("bad payload: %v", err)
	}
	_, err = ErasureDeps{DB: &store.DB{}}.handle(context.Background(), "", json.RawMessage(`{"organizationId":""}`), nil, nil)
	if errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("empty org: %v", err)
	}
}

func TestLooksPersonalAndIDOnly(t *testing.T) {
	if !looksPersonal("a@b.c") || looksPersonal(store.NewID(store.PrefixAccount)) {
		t.Fatal("looksPersonal")
	}
	if !looksPersonal("long free text with spaces") || looksPersonal("active") {
		t.Fatal("enums / free text")
	}
	if !looksPersonal(string(make([]byte, 50))) {
		t.Fatal("long non-id")
	}
	if idOnly("not-an-id") != "" {
		t.Fatal("idOnly empty")
	}
	jid := store.NewID(store.PrefixJob)
	if idOnly(jid) != jid {
		t.Fatal("idOnly job")
	}
	if looksPersonal("org:" + store.NewID(store.PrefixOrganization)) {
		t.Fatal("org settings id flagged")
	}
}

func TestHandleListAndRevokeErrors(t *testing.T) {
	// DB pointer non-nil but unused when deps fail early on ListAccountIDs.
	deps := ErasureDeps{
		DB:             &store.DB{},
		ListAccountIDs: func(context.Context, string) ([]string, error) { return nil, errors.New("list fail") },
	}
	payload, _ := json.Marshal(map[string]string{"organizationId": store.NewID(store.PrefixOrganization)})
	_, err := deps.handle(context.Background(), "job_1", payload, nil, nil)
	if errs.CodeOf(err) != errs.CodeInternalError {
		t.Fatalf("list: %v", err)
	}

	deps = ErasureDeps{
		DB:             &store.DB{},
		ListAccountIDs: func(context.Context, string) ([]string, error) { return []string{"acc_1"}, nil },
		EraseAccount: func(context.Context, string, string, func() bool) error {
			return errors.New("erase fail")
		},
	}
	_, err = deps.handle(context.Background(), "job_1", payload, nil, nil)
	if err == nil || err.Error() != "erase fail" {
		t.Fatalf("erase: %v", err)
	}

	deps = ErasureDeps{
		DB:             &store.DB{},
		ListAccountIDs: func(context.Context, string) ([]string, error) { return nil, nil },
		RevokeAPIKeys:  func(context.Context, string) (int64, error) { return 0, errors.New("revoke fail") },
	}
	_, err = deps.handle(context.Background(), "job_1", payload, nil, nil)
	if errs.CodeOf(err) != errs.CodeInternalError {
		t.Fatalf("revoke: %v", err)
	}

	deps = ErasureDeps{
		DB: &store.DB{},
		ListAccountIDs: func(context.Context, string) ([]string, error) {
			return []string{"a1"}, nil
		},
	}
	_, err = deps.handle(context.Background(), "job_1", payload, func(int64, int64) {}, func() bool { return true })
	if errs.CodeOf(err) != errs.CodeConflict {
		t.Fatalf("cancel: %v", err)
	}
}

func TestOrgObjectCleanupsOrder(t *testing.T) {
	var order []string
	org := store.NewID(store.PrefixOrganization)
	payload, _ := json.Marshal(map[string]string{"organizationId": org})
	deps := ErasureDeps{
		DB: &store.DB{},
		ListAccountIDs: func(context.Context, string) ([]string, error) {
			return []string{"acc_order"}, nil
		},
		EraseAccount: func(context.Context, string, string, func() bool) error {
			order = append(order, "eraseAccount")
			return nil
		},
		RevokeAPIKeys: func(context.Context, string) (int64, error) {
			order = append(order, "revokeKeys")
			return 1, nil
		},
		OrgObjectCleanups: []func(context.Context, string) error{
			func(context.Context, string) error { order = append(order, "objects0"); return nil },
			func(context.Context, string) error { order = append(order, "objects1"); return nil },
		},
		AfterStep: func(step string) error {
			if len(step) >= 5 && step[:5] == "coll:" {
				return errors.New("stop-before-mongo")
			}
			return nil
		},
	}
	_, err := deps.handle(context.Background(), "job_1", payload, nil, nil)
	if err == nil || err.Error() != "stop-before-mongo" {
		t.Fatalf("want stop-before-mongo, got %v", err)
	}
	want := []string{"eraseAccount", "revokeKeys", "objects0", "objects1"}
	if len(order) != len(want) {
		t.Fatalf("order=%v want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order=%v want %v", order, want)
		}
	}

	deps = ErasureDeps{
		DB:             &store.DB{},
		ListAccountIDs: func(context.Context, string) ([]string, error) { return nil, nil },
		RevokeAPIKeys:  func(context.Context, string) (int64, error) { return 0, nil },
		OrgObjectCleanups: []func(context.Context, string) error{
			func(context.Context, string) error { return errors.New("s3 fail") },
		},
	}
	_, err = deps.handle(context.Background(), "job_1", payload, nil, nil)
	if errs.CodeOf(err) != errs.CodeInternalError {
		t.Fatalf("cleanup err: %v", err)
	}
}
