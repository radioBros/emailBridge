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

// HealthListener serves only /healthz and /readyz on EB_HTTP_ADDR on nodes without the api role (sync,
// webhook, submit, scheduler), so the container healthcheck (`emailbridge healthcheck`) works for every
// role split (S15 §2). It deliberately builds no API server: modules that register routes when a Server
// exists would otherwise expose them without the api role's authentication middleware.
type HealthListener struct {
	Addr string // EB_HTTP_ADDR
	srv  *http.Server
	ln   net.Listener
	done chan error
}

// Name implements app.Module.
func (h *HealthListener) Name() string { return "httpapi.health" }

// Roles implements app.Module: every node; it does nothing where the api role serves the full server.
func (h *HealthListener) Roles() []app.Role { return nil }

// Start implements app.Module.
func (h *HealthListener) Start(ctx context.Context, a *app.App) error {
	if a.Roles.Has(app.RoleAPI) {
		return nil
	}
	s := &Server{o: Options{Checkers: a.Checkers}} // health handlers only; no details without authentication
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /readyz", s.readyz)
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", h.Addr)
	if err != nil {
		return fmt.Errorf("httpapi: health listen %s: %w", h.Addr, err)
	}
	h.ln = ln
	h.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8 << 10}
	h.done = make(chan error, 1)
	go func() {
		err := h.srv.Serve(ln)
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		h.done <- err
	}()
	a.Log.Info("health endpoints listening", "addr", ln.Addr().String())
	return nil
}

// Stop implements app.Module.
func (h *HealthListener) Stop(ctx context.Context) error {
	if h.srv == nil {
		return nil
	}
	err := h.srv.Shutdown(ctx)
	if derr := <-h.done; err == nil {
		err = derr
	}
	return err
}
