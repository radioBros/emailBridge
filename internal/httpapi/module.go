package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/radiobros/emailbridge/internal/app"
)

// Module builds the Server at startup and provides it, so domain modules started after it can register
// their operations. It runs on api nodes only. The Listener module (last in the module list) serves it.
type Module struct {
	Options Options
	server  *Server
}

// Name implements app.Module.
func (m *Module) Name() string { return "httpapi" }

// Roles implements app.Module.
func (m *Module) Roles() []app.Role { return []app.Role{app.RoleAPI} }

// Start implements app.Module.
func (m *Module) Start(_ context.Context, a *app.App) error {
	if m.Options.Checkers == nil {
		m.Options.Checkers = a.Checkers
	}
	if m.Options.Version == "" {
		m.Options.Version = a.Build.Version
	}
	s, err := New(m.Options)
	if err != nil {
		return err
	}
	m.server = s
	app.Provide(a, s)
	return nil
}

// Stop implements app.Module.
func (m *Module) Stop(context.Context) error { return nil }

// Listener serves the Server on EB_HTTP_ADDR. It must be the last module so every route is registered
// before the first request, and so it stops first on shutdown (in-flight requests finish within the grace).
type Listener struct {
	srv  *http.Server
	ln   net.Listener
	done chan error
}

// Name implements app.Module.
func (l *Listener) Name() string { return "httpapi.listener" }

// Roles implements app.Module.
func (l *Listener) Roles() []app.Role { return []app.Role{app.RoleAPI} }

// Start implements app.Module.
func (l *Listener) Start(ctx context.Context, a *app.App) error {
	s, err := app.MustGet[*Server](a)
	if err != nil {
		return err
	}
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", s.o.Addr)
	if err != nil {
		return fmt.Errorf("httpapi: listen %s: %w", s.o.Addr, err)
	}
	l.ln = ln
	l.srv = &http.Server{
		Handler:           s,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}
	l.done = make(chan error, 1)
	go func() {
		err := l.srv.Serve(ln)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		l.done <- err
	}()
	a.Log.Info("http listening", "addr", ln.Addr().String())
	return nil
}

// Addr returns the bound address (useful with ":0" in tests).
func (l *Listener) Addr() string {
	if l.ln == nil {
		return ""
	}
	return l.ln.Addr().String()
}

// Stop shuts the server down gracefully within ctx.
func (l *Listener) Stop(ctx context.Context) error {
	if l.srv == nil {
		return nil
	}
	err := l.srv.Shutdown(ctx)
	if serveErr := <-l.done; serveErr != nil && err == nil {
		err = serveErr
	}
	return err
}
