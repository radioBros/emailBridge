package tenancy

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

var (
	reCollectionCall = regexp.MustCompile(`\.Collection\(\s*"([A-Za-z][A-Za-z0-9]*)"\s*`)
	reCollConst      = regexp.MustCompile(`(?m)(?:const|var)\s+(\w*[Cc]oll(?:ection)?\w*|\w+Collection)\s*=\s*"([A-Za-z][A-Za-z0-9]*)"`)
	reNamedColl      = regexp.MustCompile(`(?m)(?:const|var)\s+\w+\s*=\s*"(accounts|organizations|users|memberships|userSettings|settings|migrations|locks|dataKeys|auditLog|jobs|events|messages|mailboxes|outboundMessages|capturedMessages|archivedMessages|attachments|idempotencyKeys|deliveries|webhookEndpoints|routes|routeVersions|ruleErrors|templates|templateVersions|smtpGateways|suppressionLists|suppressionEntries|integrationInstances|apiKeys|exports|msaCredentials|imapProxyCredentials|gmailSyncState|graphSyncState|trackingLinks|trackingEvents|deliveryTests|aiConfigs|oauthApps|oidcProviders|connectLinks|drafts)"`)
)

func TestCollectionRegistryComplete(t *testing.T) {
	seen := map[string]int{}
	for _, e := range CollectionRegistry {
		seen[e.Name]++
		if e.Name == "" {
			t.Fatal("empty collection name in registry")
		}
		if seen[e.Name] > 1 {
			t.Fatalf("duplicate registry entry: %s", e.Name)
		}
		switch e.Class {
		case EraseByOrganization, EraseByAccount, EraseBoth, EraseExcluded:
		default:
			t.Fatalf("%s: unknown class %q", e.Name, e.Class)
		}
		if e.Class == EraseExcluded && strings.TrimSpace(e.Reason) == "" {
			t.Fatalf("%s: excluded without reason", e.Name)
		}
		if e.Class == EraseByAccount || e.Class == EraseBoth {
			key := e.AccountKey
			if key == "" {
				key = AccountKeyAccountID
			}
			if key != AccountKeyAccountID && key != AccountKeyID {
				t.Fatalf("%s: bad AccountKey %q", e.Name, e.AccountKey)
			}
		}
	}

	root := packageDir(t)
	internalRoot := filepath.Clean(filepath.Join(root, ".."))
	found := map[string]struct{}{}
	err := filepath.WalkDir(internalRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			base := d.Name()
			if base == "testdata" || base == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		src := string(raw)
		for _, m := range reCollectionCall.FindAllStringSubmatch(src, -1) {
			found[m[1]] = struct{}{}
		}
		for _, m := range reCollConst.FindAllStringSubmatch(src, -1) {
			found[m[2]] = struct{}{}
		}
		for _, m := range reNamedColl.FindAllStringSubmatch(src, -1) {
			found[m[1]] = struct{}{}
		}
		// AST pass: string literals assigned to idents containing Collection / collCredentials.
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, raw, 0)
		if err != nil {
			return nil
		}
		ast.Inspect(f, func(n ast.Node) bool {
			vs, ok := n.(*ast.ValueSpec)
			if !ok {
				return true
			}
			for i, name := range vs.Names {
				nme := name.Name
				if !strings.Contains(strings.ToLower(nme), "collection") &&
					!strings.HasPrefix(nme, "coll") && nme != "Collection" &&
					nme != "Drafts" && nme != "Messages" && nme != "Mailboxes" &&
					nme != "ArchivedMessages" && nme != "Accounts" {
					continue
				}
				if i >= len(vs.Values) {
					continue
				}
				bl, ok := vs.Values[i].(*ast.BasicLit)
				if !ok || bl.Kind != token.STRING {
					continue
				}
				val := strings.Trim(bl.Value, `"`)
				if collNameOK(val) {
					found[val] = struct{}{}
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Ignore Limen logical table names that are prefixed at runtime (auth_sessions, …).
	ignore := map[string]struct{}{
		"sessions": {}, "verifications": {}, "two_factors": {}, "passkeys": {},
		"api_keys": {}, "rate_limits": {}, "accountsFn": {},
	}
	for name := range found {
		if _, skip := ignore[name]; skip {
			continue
		}
		if !collNameOK(name) {
			continue
		}
		if _, ok := seen[name]; !ok {
			t.Errorf("collection %q used in internal/** but missing from CollectionRegistry", name)
		}
	}
}

func TestOrgAndAccountEraseListsMatchRegistry(t *testing.T) {
	org := map[string]struct{}{}
	for _, n := range OrgEraseCollections() {
		org[n] = struct{}{}
		e, ok := RegistryByName(n)
		if !ok || (e.Class != EraseByOrganization && e.Class != EraseBoth) {
			t.Fatalf("OrgEraseCollections has %s with class %v", n, e.Class)
		}
	}
	for _, tgt := range AccountEraseTargets() {
		e, ok := RegistryByName(tgt.Coll)
		if !ok || (e.Class != EraseByAccount && e.Class != EraseBoth) {
			t.Fatalf("AccountEraseTargets has %s with class %v", tgt.Coll, e.Class)
		}
		want := e.AccountKey
		if want == "" {
			want = AccountKeyAccountID
		}
		if tgt.Key != want {
			t.Fatalf("%s key %q want %q", tgt.Coll, tgt.Key, want)
		}
		if _, alsoOrg := org[tgt.Coll]; alsoOrg && e.Class != EraseBoth {
			t.Fatalf("%s in both lists but class is %s", tgt.Coll, e.Class)
		}
	}
}

func collNameOK(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return false
		}
	}
	// Heuristic: real collection names are camelCase or known plurals, not single lowercase noise.
	if s == "name" || s == "coll" || s == "Collection" {
		return false
	}
	return true
}

func packageDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller")
	}
	return filepath.Dir(file)
}
