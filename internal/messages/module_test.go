package messages

import (
	"testing"

	"github.com/radiobros/emailbridge/internal/app"
)

func TestModuleMeta(t *testing.T) {
	m := &Module{}
	if m.Name() != "messages" {
		t.Fatal(m.Name())
	}
	if len(m.Roles()) != 1 || m.Roles()[0] != app.RoleAPI {
		t.Fatal(m.Roles())
	}
	if m.Service() != nil {
		t.Fatal("service before start")
	}
	mig := Migration(24)
	if mig.Number != 24 || mig.Name == "" || mig.Up == nil {
		t.Fatalf("%+v", mig)
	}
}

func TestToAddrViewsFlatten(t *testing.T) {
	if len(toAddrViews(nil)) != 0 {
		t.Fatal()
	}
	h := flattenHeaders(map[string][]string{"Subject": {"Hi"}, "X-Empty": {}})
	if h["Subject"] != "Hi" {
		t.Fatal(h)
	}
	if _, ok := h["X-Empty"]; ok {
		t.Fatal(h)
	}
}
