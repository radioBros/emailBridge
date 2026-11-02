package queues

import (
	"context"
	"log/slog"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/jobs"
	"github.com/radiobros/emailbridge/internal/realtime"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/submit"
	"github.com/radiobros/emailbridge/internal/sysmail"
	"github.com/radiobros/emailbridge/internal/tenancy"
	"github.com/radiobros/emailbridge/internal/webhook"
)

// Module serves the queue page API (W72) and runs the queues.cancel jobs. It needs only MongoDB: the
// sources read the domain collections directly and apply the domains' own cancel filters and updates.
// List it after jobs and realtime and before cluster (GET /v1/queues reads its counts).
type Module struct {
	Log *slog.Logger
	svc *Service
}

// Name implements app.Module.
func (m *Module) Name() string { return "queues" }

// Roles implements app.Module: every node runs the job handler; the API only on api nodes.
func (m *Module) Roles() []app.Role { return nil }

// Start implements app.Module.
func (m *Module) Start(_ context.Context, a *app.App) error {
	db, err := app.MustGet[*store.DB](a)
	if err != nil {
		return err
	}
	log := m.Log
	if log == nil {
		log = a.Log
	}
	svc := &Service{DB: db, Log: log}
	if w, ok := app.Get[*audit.Writer](a); ok {
		svc.Audit = w
	}
	if js, ok := app.Get[*jobs.Service](a); ok && js != nil {
		svc.Jobs = js
		js.Handle(jobKindCancel, svc.HandleCancelJob)
	}
	if hub, ok := app.Get[*realtime.Hub](a); ok && hub != nil {
		if err := hub.Register(realtime.Topic{
			Name: topicUpdates, Permission: tenancy.PermRead, ReadScope: "outbound",
			Params: realtime.KeyFilter("queue", false),
		}); err != nil {
			return err
		}
		svc.Hub = hub
	}
	m.svc = svc
	app.Provide(a, svc)
	if a.Roles.Has(app.RoleAPI) {
		srv, err := app.MustGet[*httpapi.Server](a)
		if err != nil {
			return err
		}
		registerAPI(srv, svc)
	}
	return nil
}

// Stop implements app.Module.
func (m *Module) Stop(context.Context) error { return nil }

// Service returns the started service (nil before Start).
func (m *Module) Service() *Service { return m.svc }

// Migration adds the queue item indexes (W72 §7): instance-wide listing and chunking by state + _id for
// outbound messages and deliveries (the organisation listing uses the existing organizationId + state + _id
// indexes), and the sysmailJobs records with their TTL.
func Migration(number int) store.Migration {
	return store.Migration{Number: number, Name: "queue item indexes", Up: func(ctx context.Context, db *store.DB) error {
		byState := mongo.IndexModel{Keys: bson.D{{Key: "state", Value: 1}, {Key: "_id", Value: 1}}}
		if err := db.EnsureIndexes(ctx, submit.Collection, byState); err != nil {
			return err
		}
		if err := db.EnsureIndexes(ctx, webhook.DeliveriesCollection, byState); err != nil {
			return err
		}
		return sysmail.EnsureRecordIndexes(ctx, db)
	}}
}
