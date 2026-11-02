package tenancy

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
)

// An API key reads only what its scopes name (S10 §3): holding some other scope is not read access.
func TestRequireReadNeedsTheResourceScope(t *testing.T) {
	key := func(scopes ...string) context.Context {
		return WithCaller(context.Background(), Caller{Kind: CallerAPIKey, APIKeyID: "key_x", OrganizationID: "org_1", Scopes: scopes})
	}
	for _, tc := range []struct {
		name   string
		scopes []string
		ok     bool
	}{
		{"unrelated write scope", []string{"outbound:write"}, false},
		{"unrelated read scope", []string{"logs:read"}, false},
		{"read", []string{"events:read"}, true},
		{"write implies read", []string{"events:write"}, true},
		{"admin", []string{"admin"}, true},
		{"granular account scope is not org-wide", []string{"accounts:read:" + store.NewID(store.PrefixAccount)}, false},
	} {
		err := RequireRead(key(tc.scopes...), "events")
		if tc.ok && err != nil {
			t.Errorf("%s: %v", tc.name, err)
		}
		if !tc.ok && errs.CodeOf(err) != errs.CodeScopeMissing {
			t.Errorf("%s: want scope_missing, got %v", tc.name, err)
		}
	}

	user := WithCaller(context.Background(), Caller{Kind: CallerUser, UserID: "usr_1", OrganizationID: "org_1", Role: RoleViewer})
	if err := RequireRead(user, "events"); err != nil {
		t.Fatalf("viewer reads: %v", err)
	}
	if err := RequireRead(context.Background(), "events"); errs.CodeOf(err) != errs.CodeUnauthenticated {
		t.Fatalf("no caller: %v", err)
	}
	if err := RequireRead(WithCaller(context.Background(), Caller{Kind: CallerSystem}), "events"); err != nil {
		t.Fatal(err)
	}
}

func TestRequireReadOnAndAccount(t *testing.T) {
	acc1 := store.NewID(store.PrefixAccount)
	acc2 := store.NewID(store.PrefixAccount)
	key := func(c Caller) context.Context {
		c.Kind, c.APIKeyID, c.OrganizationID = CallerAPIKey, "key_x", "org_1"
		return WithCaller(context.Background(), c)
	}

	granular := key(Caller{Scopes: []string{"accounts:read:" + acc1}})
	if err := RequireReadOn(granular, "accounts", acc1); err != nil {
		t.Fatal(err)
	}
	if err := RequireReadOn(granular, "accounts", acc2); errs.CodeOf(err) != errs.CodeScopeMissing {
		t.Fatalf("other account: %v", err)
	}
	// The account's record is not its mailbox: messages need messages:read.
	if err := RequireReadAccount(granular, "messages", acc1); errs.CodeOf(err) != errs.CodeScopeMissing {
		t.Fatalf("messages via account scope: %v", err)
	}

	bound := key(Caller{Scopes: []string{"messages:read"}, AccountID: acc1})
	if err := RequireReadAccount(bound, "messages", acc1); err != nil {
		t.Fatal(err)
	}
	if err := RequireReadAccount(bound, "messages", acc2); errs.CodeOf(err) != errs.CodeForbidden {
		t.Fatalf("bound key, other account: %v", err)
	}
	if err := RequireReadAccount(context.Background(), "messages", acc1); errs.CodeOf(err) != errs.CodeUnauthenticated {
		t.Fatalf("no caller: %v", err)
	}
	if err := RequireReadOn(context.Background(), "messages", acc1); errs.CodeOf(err) != errs.CodeUnauthenticated {
		t.Fatalf("no caller: %v", err)
	}
	viewer := WithCaller(context.Background(), Caller{Kind: CallerUser, UserID: "usr_1", OrganizationID: "org_1", Role: RoleViewer})
	if err := RequireReadAccount(viewer, "messages", acc2); err != nil {
		t.Fatal(err)
	}
	noRole := WithCaller(context.Background(), Caller{Kind: CallerUser, UserID: "usr_2", OrganizationID: "org_1"})
	if err := RequireReadOn(noRole, "messages", acc2); errs.CodeOf(err) != errs.CodeForbidden {
		t.Fatalf("user without role: %v", err)
	}
}

// Every read outside this package names its resource scope; a bare PermRead check would let any API key read
// (it is admin-only for keys now, so it would also refuse legitimate keys). Mechanical guard like a lint.
func TestNoBarePermReadOutsideTenancy(t *testing.T) {
	root := filepath.Join("..")
	bare := regexp.MustCompile(`tenancy\.Require(On|Account)?\([^,]+, tenancy\.PermRead`)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bare.Match(b) {
			t.Errorf("%s: use tenancy.RequireRead/RequireReadOn/RequireReadAccount with the resource scope", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestBarePermReadIsAdminOnlyForKeys(t *testing.T) {
	acc := store.NewID(store.PrefixAccount)
	key := Caller{Kind: CallerAPIKey, Scopes: []string{"events:read"}}
	if key.AllowsOn(PermRead, "") || key.AllowsOn(PermRead, store.NewID(store.PrefixEndpoint)) {
		t.Fatal("bare PermRead must fail closed for a scoped key")
	}
	if !(Caller{Kind: CallerAPIKey, Scopes: []string{"admin"}}).AllowsOn(PermRead, "") {
		t.Fatal("admin keys read everything")
	}
	if !(Caller{Kind: CallerAPIKey, Scopes: []string{"accounts:read:" + acc}}).AllowsOn(PermRead, acc) {
		t.Fatal("a granular scope on the resource itself still reads it")
	}
}

func TestRequiresCaller(t *testing.T) {
	for path, want := range map[string]bool{
		"/v1/accounts": true, "/v1/me": true, "/v1/admin/ws": true, "/v1/pec/receipts/rcp_x": true,
		"/v1/auth/signin/credential": false, "/v1/auth/oidc/start": false, "/v1/public/connect/t": false,
		"/v1/invitations/accept": false, "/v1/invitations/lookup": false, "/v1/connect-links/tok/redeem": false,
		"/v1/connect-links": true, "/v1/openapi.json": false, "/v1/openapi.yaml": false, "/e/token": false, "/callbacks/gmail": false, "/": false, "/healthz": false,
	} {
		if got := requiresCaller(path); got != want {
			t.Errorf("requiresCaller(%q) = %v, want %v", path, got, want)
		}
	}
}
