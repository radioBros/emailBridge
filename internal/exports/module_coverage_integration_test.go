//go:build integration

package exports

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/events"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/jobs"
)

func TestIntegrationModuleStart(t *testing.T) {
	db := testDB(t)
	objs := testS3(t)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := context.Background()
	a := app.New(app.Options{Roles: app.RoleSet{app.RoleAPI: true}, NodeID: "n", Log: quiet})
	app.Provide(a, db)
	app.Provide(a, objs)
	app.Provide(a, &jobs.Service{DB: db, NodeID: "n", Log: quiet})
	app.Provide(a, testCipher(t, db))
	app.Provide(a, &events.Writer{DB: db})
	app.Provide(a, &audit.Writer{DB: db})
	srv, err := httpapi.New(httpapi.Options{AccessLog: quiet, SecurityLog: quiet})
	if err != nil {
		t.Fatal(err)
	}
	app.Provide(a, srv)
	m := &Module{SignKey: []byte("module-sign-key-32-bytes!!!!!!!!"), PublicURL: "http://localhost", Log: quiet}
	if err := m.Start(ctx, a); err != nil {
		t.Fatal(err)
	}
	if m.Service() == nil {
		t.Fatal("svc")
	}
	_ = m.Stop(ctx)
}
