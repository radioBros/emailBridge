//go:build integration

package settings

import (
	"testing"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

func TestIntegrationOrganizationSwitchLock(t *testing.T) {
	db := testDB(t)
	cipher := testCipher(t, db)
	svc := NewService(db, cipher, features.DefaultRegistry(), nil)
	org := store.NewID(store.PrefixOrganization)
	admin := orgCaller(org, tenancy.RoleAdmin, false)

	_, err := svc.Patch(admin, "organization", map[string]any{
		"switches": map[string]any{
			"tracking.opens": map[string]any{"value": false, "locked": true},
		},
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	body, err := svc.Effective(admin, org)
	if err != nil {
		t.Fatal(err)
	}
	sw := body.Switches["tracking.opens"]
	if !sw.Locked || sw.Value != false {
		t.Fatalf("effective: %+v", sw)
	}

	// Org-only lockable (no global scope) may lock at organisation.
	_, err = svc.Patch(admin, "organization", map[string]any{
		"switches": map[string]any{
			"protocolLogs.enabled": map[string]any{"value": true, "locked": true},
		},
	}, "")
	if err != nil {
		t.Fatal(err)
	}

	// Non-lockable cannot lock.
	_, err = svc.Patch(admin, "organization", map[string]any{
		"switches": map[string]any{
			"webhook.signature.mode": map[string]any{"value": "bodyOnly", "locked": true},
		},
	}, "")
	if err == nil || errs.CodeOf(err) != errs.CodeForbidden {
		t.Fatalf("non-lockable lock: %v", err)
	}

	// Resolve: org lock beats account lower value.
	reg := features.DefaultRegistry()
	tr, _ := reg.Get("tracking.opens")
	got, src := features.Resolve(tr, "production", features.Values{
		Organization: &features.Setting{Value: features.BoolValue(false), Locked: true},
		Lower:        &features.Setting{Value: features.BoolValue(true)},
	})
	if src != features.SourceLockedOrganization || got.Bool == nil || *got.Bool {
		t.Fatalf("resolve under org lock: %v %s", got, src)
	}

	// Cannot lock org-only switch at global.
	owner := orgCaller(org, tenancy.RoleOwner, true)
	_, err = svc.Patch(owner, "global", map[string]any{
		"switches": map[string]any{
			"tracking.opens": map[string]any{"value": false, "locked": true},
		},
	}, "")
	if err == nil || (errs.CodeOf(err) != errs.CodeForbidden && errs.CodeOf(err) != errs.CodeSwitchUnknown) {
		// tracking.opens has no global scope → forbidden at global (or switch not allowed at scope).
		if err == nil {
			t.Fatal("expected error locking org-only switch at global")
		}
		if errs.CodeOf(err) != errs.CodeForbidden {
			t.Fatalf("global lock org-only: %v", err)
		}
	}

	list, err := svc.ListFeatureSwitches(admin)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, item := range list {
		if item.Name == "tracking.opens" {
			found = true
			if !item.Locked {
				t.Fatalf("list should show locked: %+v", item)
			}
		}
	}
	if !found {
		t.Fatal("tracking.opens missing from list")
	}
}
