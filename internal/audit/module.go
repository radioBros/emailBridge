package audit

import (
	"context"
	"log/slog"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

// Module wires the audit Writer and GET /v1/audit-log (api role).
type Module struct {
	// Log is the audit-category logger; when nil, slog.Default is used.
	Log *slog.Logger
	// Retention defaults to 365 days when zero.
	Retention time.Duration
	writer    *Writer
}

// Name implements app.Module.
func (m *Module) Name() string { return "audit" }

// Roles implements app.Module.
func (m *Module) Roles() []app.Role { return []app.Role{app.RoleAPI} }

// Start implements app.Module.
func (m *Module) Start(_ context.Context, a *app.App) error {
	db, err := app.MustGet[*store.DB](a)
	if err != nil {
		return err
	}
	srv, err := app.MustGet[*httpapi.Server](a)
	if err != nil {
		return err
	}
	ret := m.Retention
	if ret == 0 {
		ret = 365 * 24 * time.Hour
	}
	log := m.Log
	if log == nil {
		log = a.Log
	}
	m.writer = &Writer{DB: db, Log: log, Retention: ret}
	app.Provide(a, m.writer)
	app.Provide(a, tenancy.Auditor(tenancyBridge{w: m.writer}))
	m.registerAPI(srv)
	return nil
}

// tenancyBridge adapts Writer to tenancy.Auditor without a tenancy→audit import cycle.
type tenancyBridge struct{ w *Writer }

func (b tenancyBridge) Record(ctx context.Context, action, targetType, targetID string, changes map[string]tenancy.FieldDiff) error {
	ch := Changes{}
	for k, v := range changes {
		ch[k] = FieldChange{Before: v.Before, After: v.After}
	}
	return b.w.Record(ctx, action, Target{Type: targetType, ID: targetID}, ch)
}

// Stop implements app.Module.
func (m *Module) Stop(context.Context) error { return nil }

// Writer returns the started writer (nil before Start).
func (m *Module) Writer() *Writer { return m.writer }

// Migration creates auditLog indexes (S1) including the TTL on expireAt.
func Migration(number int) store.Migration {
	return store.Migration{Number: number, Name: "auditLog indexes", Up: func(ctx context.Context, db *store.DB) error {
		models := make([]mongo.IndexModel, 0, 4)
		for _, keys := range indexes() {
			models = append(models, mongo.IndexModel{Keys: keys})
		}
		models = append(models, mongo.IndexModel{
			Keys:    bson.D{{Key: "expireAt", Value: 1}},
			Options: options.Index().SetExpireAfterSeconds(0),
		})
		return db.EnsureIndexes(ctx, Collection, models...)
	}}
}
