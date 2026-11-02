package tenancy

import (
	"context"
	"testing"
)

// TestOperationsByRole covers every new organisation/member operation × role / API-key scope (S10 §4).
func TestOperationsByRole(t *testing.T) {
	type gate struct {
		perm         Permission
		touchOwners  bool // invite/change/remove owners
		instanceOnly bool // list/create organisations
		orgDelete    bool // delete organisation (instance owner or org owner)
	}
	ops := map[string]gate{
		"listOrganizations":  {instanceOnly: true},
		"createOrganization": {instanceOnly: true},
		"getOrganization":    {perm: PermRead},
		"patchOrganization":  {perm: PermSettingsWrite},
		"deleteOrganization": {orgDelete: true},
		"listMembers":        {perm: PermRead},
		"createMember":       {perm: PermMembersWrite},
		"createMemberOwner":  {perm: PermMembersWrite, touchOwners: true},
		"patchMember":        {perm: PermMembersWrite},
		"patchMemberOwner":   {perm: PermMembersWrite, touchOwners: true},
		"deleteMember":       {perm: PermMembersWrite},
		"deleteMemberOwner":  {perm: PermMembersWrite, touchOwners: true},
	}
	callers := map[string]Caller{
		"owner":         {Kind: CallerUser, UserID: "u", OrganizationID: "org", Role: RoleOwner},
		"admin":         {Kind: CallerUser, UserID: "u", OrganizationID: "org", Role: RoleAdmin},
		"operator":      {Kind: CallerUser, UserID: "u", OrganizationID: "org", Role: RoleOperator},
		"viewer":        {Kind: CallerUser, UserID: "u", OrganizationID: "org", Role: RoleViewer},
		"instanceOwner": {Kind: CallerUser, UserID: "u", OrganizationID: "org", Role: RoleViewer, InstanceOwner: true},
		"keyMembers":    {Kind: CallerAPIKey, APIKeyID: "k", OrganizationID: "org", Scopes: []string{"members:write"}},
		"keyAdmin":      {Kind: CallerAPIKey, APIKeyID: "k", OrganizationID: "org", Scopes: []string{"admin"}},
		"keyLogs":       {Kind: CallerAPIKey, APIKeyID: "k", OrganizationID: "org", Scopes: []string{"logs:read"}},
		"keySettings":   {Kind: CallerAPIKey, APIKeyID: "k", OrganizationID: "org", Scopes: []string{"settings:write"}},
	}

	allows := func(c Caller, g gate) bool {
		if g.instanceOnly {
			return c.InstanceOwner
		}
		if g.orgDelete {
			return c.InstanceOwner || c.Allows(PermOrgDelete)
		}
		if !c.Allows(g.perm) {
			return false
		}
		if g.touchOwners && !mayTouchOwners(c) {
			return false
		}
		return true
	}

	for opName, g := range ops {
		for callerName, c := range callers {
			ctx := WithCaller(context.Background(), c)
			got := allows(c, g)
			// exercise Require path for non-special gates
			if !g.instanceOnly && !g.orgDelete {
				err := Require(ctx, g.perm)
				reqOK := err == nil
				if g.touchOwners {
					reqOK = reqOK && mayTouchOwners(c)
				}
				if reqOK != got {
					t.Fatalf("%s/%s: Require path %v vs allows %v", opName, callerName, reqOK, got)
				}
			}
			// Spot-check expected denials from S10 §4
			switch {
			case opName == "createMemberOwner" && callerName == "admin" && got:
				t.Fatalf("admin must not invite owners")
			case opName == "patchMemberOwner" && callerName == "admin" && got:
				t.Fatalf("admin must not change owners")
			case opName == "deleteMemberOwner" && callerName == "admin" && got:
				t.Fatalf("admin must not remove owners")
			case opName == "listOrganizations" && callerName == "owner" && got:
				t.Fatalf("org owner is not instance owner")
			case opName == "deleteOrganization" && callerName == "admin" && got:
				t.Fatalf("admin must not delete organisation")
			case opName == "createMember" && callerName == "keyLogs" && got:
				t.Fatalf("key without members:write must not invite")
			case opName == "createMember" && callerName == "keyMembers" && !got:
				t.Fatalf("key with members:write must invite")
			case opName == "createMemberOwner" && callerName == "keyMembers" && got:
				t.Fatalf("members:write key must not invite owners")
			case opName == "createMemberOwner" && callerName == "keyAdmin" && !got:
				t.Fatalf("admin-scoped key may invite owners")
			case opName == "listMembers" && callerName == "viewer" && !got:
				t.Fatalf("viewer may list members")
			case opName == "patchOrganization" && callerName == "operator" && got:
				t.Fatalf("operator must not patch organisation")
			case opName == "deleteOrganization" && callerName == "owner" && !got:
				t.Fatalf("org owner may delete organisation")
			case opName == "deleteOrganization" && callerName == "instanceOwner" && !got:
				t.Fatalf("instance owner may delete organisation")
			case opName == "listOrganizations" && callerName == "instanceOwner" && !got:
				t.Fatalf("instance owner may list organisations")
			}
		}
	}
}
