package objstore

import (
	"context"
	"errors"

	"github.com/radiobros/emailbridge/internal/app"
)

// Module builds the object store at startup, ensures the bucket exists and provides *Store.
type Module struct {
	Options Options
	store   *Store
}

// Name implements app.Module.
func (m *Module) Name() string { return "objstore" }

// Roles implements app.Module (platform: every node).
func (m *Module) Roles() []app.Role { return nil }

// Start opens the store, ensures the bucket and provides *Store.
func (m *Module) Start(ctx context.Context, a *app.App) error {
	s, err := Open(m.Options)
	if err != nil {
		return err
	}
	if err := s.EnsureBucket(ctx); err != nil {
		return err
	}
	m.store = s
	app.Provide(a, s)
	return nil
}

// Stop implements app.Module (no persistent connections to close).
func (m *Module) Stop(context.Context) error { return nil }

// Ready reports whether the bucket exists.
func (m *Module) Ready(ctx context.Context) error {
	if m.store == nil {
		return ErrNotConnected
	}
	ok, err := m.store.BucketExists(ctx)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("objstore: bucket missing")
	}
	return nil
}

// ErrNotConnected is reported by Ready before Start succeeded.
var ErrNotConnected = errNotConnected{}

type errNotConnected struct{}

func (errNotConnected) Error() string { return "objstore: not connected" }
