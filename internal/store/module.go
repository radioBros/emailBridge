package store

import (
	"context"
	"log/slog"

	"github.com/radiobros/emailbridge/internal/app"
)

// Module connects to MongoDB at startup, runs the migrations under the lock, provides *DB to other
// modules and reports readiness. It runs on every node (no roles).
type Module struct {
	Options    Options
	Migrations []Migration
	// AllowOlderBinary is passed to the Migrator (rollback drills only).
	AllowOlderBinary bool
	db               *DB
}

// Name implements app.Module.
func (m *Module) Name() string { return "store" }

// Roles implements app.Module (platform module: every node).
func (m *Module) Roles() []app.Role { return nil }

// Start connects, migrates and provides *DB.
func (m *Module) Start(ctx context.Context, a *app.App) error {
	if m.Options.AppName == "" {
		m.Options.AppName = "emailbridge/" + a.NodeID
	}
	db, err := Connect(ctx, m.Options)
	if err != nil {
		return err
	}
	mg := &Migrator{DB: db, Holder: a.NodeID, Migrations: m.Migrations, Log: a.Log.With(slog.String("component", "store.migrate")), AllowOlderBinary: m.AllowOlderBinary}
	if err := mg.Run(ctx); err != nil {
		_ = db.Close(context.WithoutCancel(ctx))
		return err
	}
	m.db = db
	app.Provide(a, db)
	return nil
}

// Stop disconnects.
func (m *Module) Stop(ctx context.Context) error {
	if m.db == nil {
		return nil
	}
	return m.db.Close(ctx)
}

// Ready implements app.Checker.
func (m *Module) Ready(ctx context.Context) error {
	if m.db == nil {
		return ErrNotConnected
	}
	return m.db.Ping(ctx)
}

// ErrNotConnected is reported by Ready before Start succeeded.
var ErrNotConnected = errNotConnected{}

type errNotConnected struct{}

func (errNotConnected) Error() string { return "store: not connected" }
