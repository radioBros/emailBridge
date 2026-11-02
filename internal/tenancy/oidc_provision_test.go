package tenancy

import (
	"testing"

	"github.com/radiobros/emailbridge/internal/store"
)

func TestParseGroupRoleValue(t *testing.T) {
	org := store.NewID(store.PrefixOrganization)
	id, role, ok := ParseGroupRoleValue(org + ":admin")
	if !ok || id != org || role != RoleAdmin {
		t.Fatalf("%q %v %v", id, role, ok)
	}
	if _, _, ok := ParseGroupRoleValue("admins:owner"); ok {
		t.Fatal("plain role must fail")
	}
	if _, _, ok := ParseGroupRoleValue(org + ":nope"); ok {
		t.Fatal("bad role")
	}
	if _, _, ok := ParseGroupRoleValue(""); ok {
		t.Fatal("empty")
	}
}
