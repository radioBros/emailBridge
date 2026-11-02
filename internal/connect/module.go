package connect

import (
	"context"
	"log/slog"
	"time"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/attachments"
	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/autoconfig"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/objstore"
	"github.com/radiobros/emailbridge/internal/store"
)

// Module wires hosted connect + identity pinning on API nodes.
type Module struct {
	PublicURL  string
	Env        string
	Log        *slog.Logger
	Registry   *features.Registry
	Create     AccountCreateFn
	OAuthStart OAuthStartFn
	Enabled    HostedEnabledFn
	AVEnabled  AVEnabledFn
	ClamAVAddr string
	// CSRFKey signs the connect form CSRF tokens; the same key on every API node (derived from EB_AUTH_SECRET).
	CSRFKey []byte
	svc     *Service
}

// Name implements app.Module.
func (m *Module) Name() string { return "connect" }

// Roles implements app.Module.
func (m *Module) Roles() []app.Role { return []app.Role{app.RoleAPI} }

// Start implements app.Module.
func (m *Module) Start(_ context.Context, a *app.App) error {
	db, err := app.MustGet[*store.DB](a)
	if err != nil {
		return err
	}
	disco, _ := app.Get[*autoconfig.Service](a)
	if disco == nil {
		disco = autoconfig.NewService(nil)
	}
	aud, _ := app.Get[*audit.Writer](a)
	objs, _ := app.Get[*objstore.Store](a)
	reg := m.Registry
	if reg == nil {
		reg = features.DefaultRegistry()
	}
	repo := &Repo{DB: db}
	svc := NewService(repo, disco)
	svc.Assets = repo
	svc.Objects = objs
	svc.Audit = aud
	if m.ClamAVAddr != "" {
		svc.Scanner = &attachments.Scanner{Addr: m.ClamAVAddr, Timeout: 30 * time.Second, MaxSize: maxLogoBytes}
	} else if att, ok := app.Get[*attachments.Service](a); ok && att != nil && att.Scanner != nil {
		svc.Scanner = att.Scanner
	}
	if len(m.CSRFKey) > 0 {
		svc.CSRFKey = m.CSRFKey
	}
	svc.Registry = reg
	svc.PublicURL = m.PublicURL
	svc.Env = m.Env
	svc.Create = m.Create
	svc.OAuthStart = m.OAuthStart
	if m.Enabled != nil {
		svc.Enabled = m.Enabled
	}
	if m.AVEnabled != nil {
		svc.AVEnabled = m.AVEnabled
	}
	m.svc = svc
	app.Provide(a, svc)
	if !a.Roles.Has(app.RoleAPI) {
		return nil
	}
	server, err := app.MustGet[*httpapi.Server](a)
	if err != nil {
		return err
	}
	m.registerAPI(server)
	return nil
}

// Stop implements app.Module.
func (m *Module) Stop(context.Context) error { return nil }

// Service returns the started service.
func (m *Module) Service() *Service { return m.svc }
