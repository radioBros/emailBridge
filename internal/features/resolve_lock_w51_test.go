package features

import "testing"

func TestResolveOrganizationLock(t *testing.T) {
	t.Parallel()
	orgAcct := Switch{
		Name: "tracking.opens", Scopes: []Scope{ScopeOrganization, ScopeAccount},
		Default: off, Lockable: true,
	}
	allScopes := Switch{
		Name: "sandbox.enabled", Scopes: []Scope{ScopeGlobal, ScopeOrganization, ScopeAccount},
		Default: off, Lockable: true,
	}
	cases := []struct {
		name   string
		sw     Switch
		v      Values
		want   Value
		source Source
	}{
		{
			"org lock beats lower", orgAcct,
			Values{
				Organization: &Setting{Value: off, Locked: true},
				Lower:        &Setting{Value: on},
			},
			off, SourceLockedOrganization,
		},
		{
			"global lock beats org lock", allScopes,
			Values{
				Global:       &Setting{Value: on, Locked: true},
				Organization: &Setting{Value: off, Locked: true},
				Lower:        &Setting{Value: off},
			},
			on, SourceLockedGlobal,
		},
		{
			"unlocked org loses to lower", orgAcct,
			Values{
				Organization: &Setting{Value: off},
				Lower:        &Setting{Value: on},
			},
			on, SourceLower,
		},
		{
			"org lock without lower", orgAcct,
			Values{Organization: &Setting{Value: on, Locked: true}},
			on, SourceLockedOrganization,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, src := Resolve(tc.sw, "production", tc.v)
			if src != tc.source || !got.Equal(tc.want) {
				t.Fatalf("got %v %s want %v %s", got, src, tc.want, tc.source)
			}
		})
	}
}

func TestResolveOrgOnlyLockable(t *testing.T) {
	t.Parallel()
	// Lockable without global scope (protocolLogs-style).
	sw := Switch{
		Name: "protocolLogs.enabled", Scopes: []Scope{ScopeOrganization, ScopeAccount},
		Default: off, Lockable: true,
	}
	got, src := Resolve(sw, "production", Values{
		Organization: &Setting{Value: on, Locked: true},
		Lower:        &Setting{Value: off},
	})
	if src != SourceLockedOrganization || !got.Equal(on) {
		t.Fatalf("got %v %s", got, src)
	}
	// Locked flag ignored when not Lockable.
	non := Switch{Name: "x", Scopes: []Scope{ScopeOrganization, ScopeAccount}, Default: off}
	got, src = Resolve(non, "production", Values{
		Organization: &Setting{Value: on, Locked: true},
		Lower:        &Setting{Value: off},
	})
	if src != SourceLower || !got.Equal(off) {
		t.Fatalf("non-lockable: %v %s", got, src)
	}
}

func TestCatalogueOrgScopesForAccountLockables(t *testing.T) {
	t.Parallel()
	r := DefaultRegistry()
	for _, name := range []string{"protocolLogs.enabled", "protocolLogs.rawDebug", "accounts.ignoreCertErrors"} {
		sw, ok := r.Get(name)
		if !ok || !sw.Lockable || !Allows(sw, ScopeOrganization) || !Allows(sw, ScopeAccount) {
			t.Fatalf("%s scopes=%v lockable=%v", name, sw.Scopes, sw.Lockable)
		}
	}
}
