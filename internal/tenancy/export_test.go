package tenancy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/store"
)

func TestMeExportAuthAndRate(t *testing.T) {
	deps := MeExportDeps{DB: &store.DB{}, Repo: &Repo{}}
	if _, err := deps.Export(context.Background()); errs.CodeOf(err) != errs.CodeUnauthenticated {
		t.Fatalf("anon: %v", err)
	}
	keyCtx := WithCaller(context.Background(), Caller{Kind: CallerAPIKey, APIKeyID: "key_1", OrganizationID: "org_x"})
	if _, err := deps.Export(keyCtx); errs.CodeOf(err) != errs.CodeForbidden {
		t.Fatalf("api key: %v", err)
	}
	userCtx := WithCaller(context.Background(), Caller{Kind: CallerUser, UserID: "usr_x", AuthUserID: "au"})
	deps.Rate = func(context.Context) error {
		return errs.New(errs.CodeRateLimited, "Too many export attempts; wait and try again.")
	}
	if _, err := deps.Export(userCtx); errs.CodeOf(err) != errs.CodeRateLimited {
		t.Fatalf("rate: %v", err)
	}
	deps.Rate = nil
	if _, err := deps.Export(userCtx); errs.CodeOf(err) != errs.CodeInternalError {
		t.Fatalf("nil repo db path: %v", err)
	}
	RegisterMeExport(nil, deps) // no-op
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv, err := httpapi.New(httpapi.Options{AccessLog: quiet, SecurityLog: quiet})
	if err != nil {
		t.Fatal(err)
	}
	RegisterMeExport(srv, deps)
	if _, err := deps.handle(context.Background(), nil); errs.CodeOf(err) != errs.CodeUnauthenticated {
		t.Fatalf("handle anon: %v", err)
	}
}

func TestMeExportSessionsPasskeysErrors(t *testing.T) {
	_ = MeExportDeps{
		DB: &store.DB{}, Repo: &Repo{DB: &store.DB{}},
		Sessions: func(context.Context, string, string) ([]ExportSession, error) {
			return nil, errors.New("sess boom")
		},
	}
	if _, ok := RegistryByName("messages"); !ok {
		t.Fatal("messages missing")
	}
	if _, ok := RegistryByName("no-such-coll"); ok {
		t.Fatal("unknown found")
	}
	if len(AccountEraseTargets()) == 0 || len(OrgEraseCollections()) == 0 {
		t.Fatal("erase lists empty")
	}
}

func TestScrubExportChanges(t *testing.T) {
	in := map[string]any{
		"name":       map[string]any{"after": "Ada"},
		"password":   map[string]any{"after": "secret"},
		"totpSecret": map[string]any{"before": "x"},
	}
	out := scrubExportChanges(in)
	if out["password"].(map[string]any)["after"] != "[redacted]" {
		t.Fatalf("password: %+v", out["password"])
	}
	if out["totpSecret"].(map[string]any)["before"] != "[redacted]" {
		t.Fatalf("totp: %+v", out["totpSecret"])
	}
	if out["name"].(map[string]any)["after"] != "Ada" {
		t.Fatalf("name scrubbed: %+v", out["name"])
	}
	if scrubExportChanges(nil) != nil {
		t.Fatal("nil")
	}
}

func TestExportCursorHMAC(t *testing.T) {
	key := []byte("test-me-export-cursor-key")
	start := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	c := exportCursor{At: start.Add(-time.Minute), ID: "aud_1", User: "usr_a", Start: start}
	tok := encodeExportCursor(key, c)
	got, err := decodeExportCursor(key, "usr_a", tok)
	if err != nil || got.ID != c.ID || !got.Start.Equal(start) {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := decodeExportCursor(key, "usr_b", tok); err == nil {
		t.Fatal("other user")
	}
	if _, err := decodeExportCursor(key, "usr_a", tok+"x"); err == nil {
		t.Fatal("tampered")
	}
	if _, err := decodeExportCursor(nil, "usr_a", tok); err == nil {
		t.Fatal("no key")
	}
}

func TestMeExportJSONOmitsSecrets(t *testing.T) {
	u := &User{
		ID: store.NewID(store.PrefixUser), Email: "a@b.test", Name: "Ada", Locale: "en",
		Status: "active", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(), Version: 1,
	}
	body := MeExportBody{
		Profile:     u,
		Memberships: []MeMembership{{OrganizationID: "org_x", OrganizationName: "X", Role: RoleOwner}},
		Settings:    map[string]any{"tables": map[string]any{}},
		Sessions:    []ExportSession{{ID: "s1", Method: "password", CreatedAt: time.Now().UTC(), LastActiveAt: time.Now().UTC(), ExpiresAt: time.Now().UTC()}},
		Passkeys:    []ExportPasskey{{ID: "pk1", Name: "Laptop"}},
	}
	body.Audit.Data = []ExportAuditRecord{}
	body.Audit.Limit = MeExportAuditLimit
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	for _, bad := range []string{"passwordHash", "totpSecret", "backupCodes", `"token"`, "clientSecret"} {
		if strings.Contains(s, bad) {
			t.Fatalf("secret-looking field %q in export: %s", bad, s)
		}
	}
}
