// Package app runs EmailBridge: it parses the roles a node runs, starts every module that belongs to those
// roles in order, and stops them in reverse order on shutdown (S0 §3.1, PLAN.md "Roles").
package app

import (
	"fmt"
	"slices"
	"strings"
)

// Role is one of the five process roles of the single binary.
type Role string

const (
	RoleAPI       Role = "api"
	RoleSync      Role = "sync"
	RoleSubmit    Role = "submit"
	RoleWebhook   Role = "webhook"
	RoleScheduler Role = "scheduler"
)

// AllRoles lists every role in start order.
var AllRoles = []Role{RoleAPI, RoleSync, RoleSubmit, RoleWebhook, RoleScheduler}

// RoleSet is the set of roles a node runs.
type RoleSet map[Role]bool

// ParseRoles parses EB_ROLES: "all" or a comma-separated list of role names.
func ParseRoles(s string) (RoleSet, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	set := RoleSet{}
	if s == "" || s == "all" {
		for _, r := range AllRoles {
			set[r] = true
		}
		return set, nil
	}
	for _, part := range strings.Split(s, ",") {
		r := Role(strings.TrimSpace(part))
		if r == "" {
			continue
		}
		if r == "all" {
			return nil, fmt.Errorf("roles: %q cannot be combined with other roles", "all")
		}
		if !slices.Contains(AllRoles, r) {
			return nil, fmt.Errorf("roles: unknown role %q (valid: all, api, sync, submit, webhook, scheduler)", r)
		}
		set[r] = true
	}
	if len(set) == 0 {
		return nil, fmt.Errorf("roles: no role given")
	}
	return set, nil
}

// Has reports whether the set contains r.
func (s RoleSet) Has(r Role) bool { return s[r] }

// Any reports whether the set contains at least one of roles. An empty roles list matches every set
// (platform modules run on every node).
func (s RoleSet) Any(roles []Role) bool {
	if len(roles) == 0 {
		return true
	}
	for _, r := range roles {
		if s[r] {
			return true
		}
	}
	return false
}

// String renders the set in canonical order, e.g. "api,webhook".
func (s RoleSet) String() string {
	var parts []string
	for _, r := range AllRoles {
		if s[r] {
			parts = append(parts, string(r))
		}
	}
	return strings.Join(parts, ",")
}
