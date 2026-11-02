package settings

import (
	"context"
	"log/slog"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/config"
	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/store"
)

// Module wires runtime settings, feature switches and user settings APIs (api role).
type Module struct {
	Boot       *config.Config
	EnvSources map[string]config.Source
	Registry   *features.Registry
	Notify     Notifier
	Log        *slog.Logger
	svc        *Service
}

// Name implements app.Module.
func (m *Module) Name() string { return "settings" }

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
	cipher, _ := app.Get[*crypto.Cipher](a)
	reg := m.Registry
	if reg == nil {
		reg = features.DefaultRegistry()
	}
	m.svc = NewService(db, cipher, reg, m.Boot)
	m.svc.EnvSources = m.EnvSources
	m.svc.Notify = m.Notify
	app.Provide(a, m.svc)
	m.registerAPI(srv)
	return nil
}

// Stop implements app.Module.
func (m *Module) Stop(context.Context) error { return nil }

// Service returns the started service (nil before Start).
func (m *Module) Service() *Service { return m.svc }

// Migration creates settings and userSettings indexes.
func Migration(number int) store.Migration {
	return store.Migration{Number: number, Name: "settings indexes", Up: func(ctx context.Context, db *store.DB) error {
		if err := db.EnsureIndexes(ctx, SettingsCollection, mongo.IndexModel{
			Keys: bson.D{{Key: "_id", Value: 1}},
		}); err != nil {
			return err
		}
		return db.EnsureIndexes(ctx, UserSettingsCollection, mongo.IndexModel{
			Keys:    bson.D{{Key: "updatedAt", Value: -1}},
			Options: options.Index(),
		})
	}}
}
