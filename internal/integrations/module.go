package integrations

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/scheduler"
	"github.com/radiobros/emailbridge/internal/store"
)

// Module wires the integrations framework on API nodes (and digests on the scheduler).
type Module struct {
	Log      *slog.Logger
	Registry *Registry
	// Optional resource adapters (wired from cmd).
	Endpoints   EndpointResources
	Routes      RouteResources
	Settings    SettingsResources
	Gateways    GatewayResources
	Attachments AttachmentReader
	Submit      OutboundSubmitter
	svc         *Service
}

// Name implements app.Module.
func (m *Module) Name() string { return "integrations" }

// Roles implements app.Module — nil = all roles (API routes + digest duty registration).
func (m *Module) Roles() []app.Role { return nil }

// Start implements app.Module.
func (m *Module) Start(_ context.Context, a *app.App) error {
	db, err := app.MustGet[*store.DB](a)
	if err != nil {
		return err
	}
	cipher, err := app.MustGet[*crypto.Cipher](a)
	if err != nil {
		return err
	}
	log := m.Log
	if log == nil {
		log = a.Log
	}
	_ = log
	reg := m.Registry
	if reg == nil {
		reg = DefaultRegistry()
	}
	if fr, ok := app.Get[*features.Registry](a); ok && fr != nil {
		_ = registerConnectorSwitches(fr, reg)
	}
	svc := &Service{
		Repo: &Repo{DB: db}, Registry: reg, Cipher: cipher, DB: db,
		Endpoints: m.Endpoints, Routes: m.Routes, Settings: m.Settings,
		Gateways: m.Gateways, Attachments: m.Attachments, Submit: m.Submit,
		limiter: newCallbackLimiter(60),
	}
	if aud, ok := app.Get[*audit.Writer](a); ok {
		svc.Audit = aud
	}
	if d, ok := app.Get[*egress.Dialer](a); ok && d != nil {
		svc.HTTP = d.HTTPClient(15 * time.Second) // connector test/install calls reach tenant-set URLs (S10 §8)
	}
	m.svc = svc
	app.Provide(a, svc)
	if a.Roles.Has(app.RoleAPI) {
		server, err := app.MustGet[*httpapi.Server](a)
		if err != nil {
			return err
		}
		m.registerAPI(server)
		server.Router.Post("/integrations/{instanceId}/callback", m.rawCallback)
		documentCallback(server)
	}
	if sched, ok := app.Get[*scheduler.Scheduler](a); ok && sched != nil {
		sched.Add("integrations.chat-digest", svc.DigestDuty)
	}
	return nil
}

// Stop implements app.Module.
func (m *Module) Stop(context.Context) error { return nil }

// Service returns the started service.
func (m *Module) Service() *Service { return m.svc }

// Migration creates integrationInstances indexes. Claimed number: 35 (wire in modules.go).
func Migration(number int) store.Migration {
	return store.Migration{Number: number, Name: "integrationInstances indexes", Up: EnsureIndexes}
}

func registerConnectorSwitches(fr *features.Registry, reg *Registry) error {
	var off = features.BoolValue(false)
	for _, d := range reg.All() {
		name := SwitchName(d.ID)
		if _, ok := fr.Get(name); ok {
			continue
		}
		if err := fr.Register(features.Switch{
			Name: name, Description: "Connector " + d.Name,
			Scopes: []features.Scope{features.ScopeOrganization}, Default: off, Phase: "P8", Lockable: true,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (m *Module) rawCallback(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "instanceId")
	err := m.svc.HandleCallback(r.Context(), id, r)
	if err != nil {
		httpapi.WriteProblem(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
