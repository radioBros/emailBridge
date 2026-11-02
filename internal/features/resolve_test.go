package features

import (
	"testing"
)

func TestRegisterErrors(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	sw := Switch{Name: "a", Scopes: []Scope{ScopeGlobal}, Default: off}
	if err := r.Register(sw); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(sw); err == nil {
		t.Fatal("duplicate")
	}
	if err := r.Register(Switch{Name: "b", Scopes: nil}); err == nil {
		t.Fatal("empty scopes")
	}
	if err := r.Register(Switch{Name: "", Scopes: []Scope{ScopeGlobal}}); err == nil {
		t.Fatal("empty name")
	}
}

func TestGetAll(t *testing.T) {
	t.Parallel()
	r := DefaultRegistry()
	all := r.All()
	if len(all) < 40 {
		t.Fatalf("too few switches: %d", len(all))
	}
	for i := 1; i < len(all); i++ {
		if all[i-1].Name >= all[i].Name {
			t.Fatalf("not sorted: %s >= %s", all[i-1].Name, all[i].Name)
		}
	}
	if _, ok := r.Get("sandbox.enabled"); !ok {
		t.Fatal("missing sandbox.enabled")
	}
}

func TestResolve(t *testing.T) {
	t.Parallel()
	sw := Switch{
		Name: "x", Scopes: []Scope{ScopeGlobal, ScopeOrganization, ScopeAccount},
		Default: off, Lockable: true,
	}
	cases := []struct {
		name   string
		env    string
		v      Values
		want   Value
		source Source
	}{
		{"default", "production", Values{}, off, SourceDefault},
		{
			"global", "production",
			Values{Global: &Setting{Value: on}},
			on, SourceGlobal,
		},
		{
			"org over global", "production",
			Values{
				Global:       &Setting{Value: on},
				Organization: &Setting{Value: off},
			},
			off, SourceOrganization,
		},
		{
			"lower wins", "production",
			Values{
				Global:       &Setting{Value: on},
				Organization: &Setting{Value: on},
				Lower:        &Setting{Value: off},
			},
			off, SourceLower,
		},
		{
			"locked global wins", "production",
			Values{
				Global:       &Setting{Value: on, Locked: true},
				Organization: &Setting{Value: off},
				Lower:        &Setting{Value: off},
			},
			on, SourceLockedGlobal,
		},
		{
			"ignore disallowed org", "production",
			Values{Organization: &Setting{Value: on}},
			// sw allows org — this one should win
			on, SourceOrganization,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, src := Resolve(sw, tc.env, tc.v)
			if src != tc.source || !got.Equal(tc.want) {
				t.Fatalf("got %v %s want %v %s", got, src, tc.want, tc.source)
			}
		})
	}

	// Scope not allowed: switch with only account scope ignores global.
	acctOnly := Switch{Name: "y", Scopes: []Scope{ScopeAccount}, Default: off}
	got, src := Resolve(acctOnly, "production", Values{Global: &Setting{Value: on}})
	if src != SourceDefault || !got.Equal(off) {
		t.Fatalf("disallowed global: %v %s", got, src)
	}

	// Locked on non-lockable is ignored for lock win; still uses global as normal.
	nonLock := Switch{Name: "z", Scopes: []Scope{ScopeGlobal, ScopeAccount}, Default: off, Lockable: false}
	got, src = Resolve(nonLock, "production", Values{
		Global: &Setting{Value: on, Locked: true},
		Lower:  &Setting{Value: off},
	})
	if src != SourceLower || !got.Equal(off) {
		t.Fatalf("non-lockable locked: %v %s", got, src)
	}
}

func TestSandboxDefaults(t *testing.T) {
	t.Parallel()
	r := DefaultRegistry()
	sw, _ := r.Get("sandbox.enabled")
	if !sw.EffectiveDefault("production").Equal(off) {
		t.Fatal("prod should be off")
	}
	if !sw.EffectiveDefault("development").Equal(on) {
		t.Fatal("dev should be on")
	}
	pol, _ := r.Get("sandbox.recipientPolicy")
	if !pol.EffectiveDefault("production").Equal(StringValue("allow")) {
		t.Fatal("prod policy")
	}
	if !pol.EffectiveDefault("staging").Equal(StringValue("capture")) {
		t.Fatal("staging policy")
	}
}
