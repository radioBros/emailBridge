package protolog

import (
	"context"
	"testing"

	"github.com/radiobros/emailbridge/internal/app"
)

func TestModuleIdentity(t *testing.T) {
	m := &Module{}
	if m.Name() != "protolog" {
		t.Fatal(m.Name())
	}
	if roles := m.Roles(); len(roles) != 1 || roles[0] != app.RoleAPI {
		t.Fatalf("%v", roles)
	}
	if err := m.Stop(context.TODO()); err != nil {
		t.Fatal(err)
	}
	if m.API() != nil {
		t.Fatal("api before start")
	}
}

func TestAppendUnique(t *testing.T) {
	got := appendUnique(nil, "a", "b", "a")
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("%v", got)
	}
}
