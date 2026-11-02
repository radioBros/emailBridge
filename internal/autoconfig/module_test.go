package autoconfig

import (
	"context"
	"testing"

	"github.com/radiobros/emailbridge/internal/app"
)

func TestModuleStartStopWithoutAPI(t *testing.T) {
	m := &Module{}
	a := app.New(app.Options{NodeID: "n1", Roles: app.RoleSet{}})
	if err := m.Start(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if m.Service() == nil {
		t.Fatal("service")
	}
	_ = m.Stop(context.Background())
}
