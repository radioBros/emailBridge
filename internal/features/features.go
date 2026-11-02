// Package features is the pure-Go feature-switch registry (S9 §4).
package features

import (
	"fmt"
	"sort"
	"sync"
)

// Scope is a level at which a switch may be set.
type Scope string

const (
	// ScopeGlobal is the instance-wide scope.
	ScopeGlobal Scope = "global"
	// ScopeOrganization is per organisation.
	ScopeOrganization Scope = "organization"
	// ScopeAccount is per mail account.
	ScopeAccount Scope = "account"
	// ScopeEndpoint is per webhook endpoint.
	ScopeEndpoint Scope = "endpoint"
)

// Value holds either a boolean or a string enum.
type Value struct {
	Bool   *bool
	String *string
}

// BoolValue returns a boolean Value.
func BoolValue(v bool) Value {
	b := v
	return Value{Bool: &b}
}

// StringValue returns a string enum Value.
func StringValue(v string) Value {
	s := v
	return Value{String: &s}
}

// IsBool reports whether the value is boolean.
func (v Value) IsBool() bool { return v.Bool != nil }

// IsString reports whether the value is a string enum.
func (v Value) IsString() bool { return v.String != nil }

// Equal reports whether two values are equal.
func (v Value) Equal(o Value) bool {
	switch {
	case v.Bool != nil && o.Bool != nil:
		return *v.Bool == *o.Bool
	case v.String != nil && o.String != nil:
		return *v.String == *o.String
	default:
		return false
	}
}

// Display renders the value for logs and tests.
func (v Value) Display() string {
	if v.Bool != nil {
		return fmt.Sprintf("Bool(%v)", *v.Bool)
	}
	if v.String != nil {
		return fmt.Sprintf("String(%q)", *v.String)
	}
	return "Unset"
}

// Switch describes one feature switch in the registry.
type Switch struct {
	Name        string
	Description string
	Scopes      []Scope
	Default     Value
	// DefaultFor, when set, returns the default for a given EB_ENV (e.g. sandbox.*).
	// When nil, Default is used.
	DefaultFor func(env string) Value
	Phase      string
	Lockable   bool
}

// EffectiveDefault returns the default for env.
func (s Switch) EffectiveDefault(env string) Value {
	if s.DefaultFor != nil {
		return s.DefaultFor(env)
	}
	return s.Default
}

// Source identifies which layer won during Resolve.
type Source string

const (
	// SourceDefault means the catalogue default was used.
	SourceDefault Source = "default"
	// SourceGlobal means the global setting won.
	SourceGlobal Source = "global"
	// SourceOrganization means the organisation setting won.
	SourceOrganization Source = "organization"
	// SourceLower means the account/endpoint setting won.
	SourceLower Source = "lower"
	// SourceLockedGlobal means a locked global value forced the result.
	SourceLockedGlobal Source = "locked_global"
	// SourceLockedOrganization means a locked organisation value forced the result (W51).
	SourceLockedOrganization Source = "locked_organization"
)

// Setting is a value set at one scope, optionally locked (global or organisation).
type Setting struct {
	Value  Value
	Locked bool
}

// Values holds the layered settings for Resolve.
type Values struct {
	Global       *Setting
	Organization *Setting
	Lower        *Setting // account or endpoint
}

// Registry is a name → Switch map.
type Registry struct {
	mu sync.RWMutex
	by map[string]Switch
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{by: make(map[string]Switch)}
}

// Register adds switches. Duplicate names and empty scopes are errors.
func (r *Registry) Register(switches ...Switch) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range switches {
		if s.Name == "" {
			return fmt.Errorf("feature switch: empty name")
		}
		if len(s.Scopes) == 0 {
			return fmt.Errorf("feature switch %q: empty scopes", s.Name)
		}
		if _, ok := r.by[s.Name]; ok {
			return fmt.Errorf("feature switch %q: duplicate", s.Name)
		}
		r.by[s.Name] = s
	}
	return nil
}

// Get returns a switch by name.
func (r *Registry) Get(name string) (Switch, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.by[name]
	return s, ok
}

// All returns all switches sorted by name.
func (r *Registry) All() []Switch {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Switch, 0, len(r.by))
	for _, s := range r.by {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Allows reports whether scope is in the switch's Scopes.
func Allows(sw Switch, scope Scope) bool {
	for _, s := range sw.Scopes {
		if s == scope {
			return true
		}
	}
	return false
}

// Resolve picks the effective value.
// Most specific set value wins (lower → organization → global → default).
// A locked higher scope on a lockable switch wins over every lower scope (W51).
// Values at a scope the switch does not allow are ignored.
func Resolve(sw Switch, env string, v Values) (Value, Source) {
	if sw.Lockable && v.Global != nil && v.Global.Locked && Allows(sw, ScopeGlobal) {
		return v.Global.Value, SourceLockedGlobal
	}
	if sw.Lockable && v.Organization != nil && v.Organization.Locked && Allows(sw, ScopeOrganization) {
		return v.Organization.Value, SourceLockedOrganization
	}
	if v.Lower != nil && (Allows(sw, ScopeAccount) || Allows(sw, ScopeEndpoint)) {
		return v.Lower.Value, SourceLower
	}
	if v.Organization != nil && Allows(sw, ScopeOrganization) {
		return v.Organization.Value, SourceOrganization
	}
	if v.Global != nil && Allows(sw, ScopeGlobal) {
		return v.Global.Value, SourceGlobal
	}
	return sw.EffectiveDefault(env), SourceDefault
}
