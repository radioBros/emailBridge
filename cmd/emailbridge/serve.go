package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/config"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/logging"
	"github.com/radiobros/emailbridge/internal/notify"
	"github.com/radiobros/emailbridge/internal/observability"
)

// serve loads the configuration, starts the log system and runs the node until ctx ends.
func serve(ctx context.Context, _ []string, stderr io.Writer) error {
	boot := slog.New(slog.NewJSONHandler(stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cfg, report, err := config.LoadFromOS()
	if err != nil {
		return err
	}
	roles, err := app.ParseRoles(strings.Join(cfg.Process.Roles, ","))
	if err != nil {
		return err
	}
	if err := cfg.Validate(roles.Has(app.RoleAPI)); err != nil {
		return err
	}
	for _, w := range report.Warnings {
		boot.Warn("configuration warning", "detail", w)
	}
	logs, err := startLogging(cfg, roles, stderr)
	if err != nil {
		return err
	}
	defer func() {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		_ = logs.Close(cctx)
		cancel()
	}()
	slog.SetDefault(logs.Default())
	sys := logs.For(logging.CatSystem, "boot")
	sys.Info("configuration loaded", "config", cfg.Redacted())

	mods, err := modules(ctx, cfg, report, logs)
	if err != nil {
		return err
	}
	a := app.New(app.Options{
		Roles:   roles,
		NodeID:  cfg.Process.NodeID,
		Build:   app.BuildInfo{Version: version, Commit: commit, Date: date},
		Log:     logs.For(logging.CatSystem, "app"),
		Grace:   cfg.Process.ShutdownGrace,
		Modules: mods,
	})
	return a.Run(ctx)
}

// startLogging creates the log system from the configuration (S12; the SQLite store attaches itself when
// its module starts).
func startLogging(cfg *config.Config, roles app.RoleSet, stderr io.Writer) (*logging.System, error) {
	st := logging.DefaultSettings()
	if cfg.Log.Level != "" {
		lvl, err := logging.ParseLevel(cfg.Log.Level)
		if err != nil {
			return nil, err
		}
		st = st.WithDefaultLevel(lvl)
	}
	st = st.WithRetention(cfg.Log.Retention)
	st.Stdout = cfg.Log.Stdout
	st.HashAddresses = cfg.Log.HashAddresses
	if cfg.Log.DiskCap > 0 {
		st.DiskCap = int64(cfg.Log.DiskCap)
	}
	hk := sha256.Sum256([]byte("emailbridge-log-hash|" + cfg.Auth.Secret))
	return logging.New(logging.Options{
		Node:     cfg.Process.NodeID,
		Role:     roles.String(),
		Settings: st,
		HashKey:  hk[:],
		Fallback: stderr,
		Classifier: func(err error) (string, string) {
			return string(errs.KindOf(err)), string(errs.CodeOf(err))
		},
	}), nil
}

// natsCredsFile writes EB_NATS_CREDS (content) to a 0600 file nats.go can read, or returns "".
func natsCredsFile(cfg *config.Config) (string, error) {
	if cfg.Stores.NATSCreds == "" {
		return "", nil
	}
	dir := filepath.Join(cfg.Process.DataDir, "run")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "nats.creds")
	if err := os.WriteFile(path, []byte(cfg.Stores.NATSCreds), 0o600); err != nil {
		return "", fmt.Errorf("write nats creds: %w", err)
	}
	return path, nil
}

// reporterRef hands the error reporter to components built before the observability module starts (the HTTP
// server is listed first); it is bound by bindReporter right after observability starts. Unbound = no-op.
type reporterRef struct {
	p atomic.Pointer[observability.Reporter]
}

func (r *reporterRef) get() *observability.Reporter {
	if r == nil {
		return nil
	}
	return r.p.Load()
}

// captureError forwards a reportable HTTP error (internal / unexpected kind) to the error reporter (S12 §12).
// It also raises an instance-scope `internal` problem for the instance owners (W75 §4), with or without a
// configured DSN.
func (r *reporterRef) captureError(ctx context.Context, err error) {
	tags := map[string]string{
		"component": "httpapi", "code": string(errs.CodeOf(err)), "kind": string(errs.KindOf(err)),
	}
	if rep := r.get(); rep != nil {
		rep.CaptureError(ctx, err, tags)
	}
	notify.Default().CaptureError(ctx, err, tags)
}

// bindReporter binds ref once the observability module has started (list it after that module).
func bindReporter(ref *reporterRef) app.Module {
	return &hook{name: "wire.reporter", start: func(_ context.Context, a *app.App) error {
		if rep, ok := app.Get[*observability.Reporter](a); ok {
			ref.p.Store(rep)
		}
		return nil
	}}
}

// panicReporter logs recovered HTTP panics in the crash category and reports them (S12 §12).
func panicReporter(logs *logging.System, ref *reporterRef) func(r *http.Request, rec any, stack []byte) {
	crash := logs.For(logging.CatCrash, "http")
	return func(r *http.Request, rec any, stack []byte) {
		crash.ErrorContext(r.Context(), "recovered panic in request", "panic", fmt.Sprint(rec), "stack", string(stack), "path", r.URL.Path)
		if rep := ref.get(); rep != nil {
			rep.CapturePanic(r.Context(), rec, stack, map[string]string{"component": "http"})
		}
		notify.Default().CapturePanic(r.Context(), rec, map[string]string{"component": "http"})
	}
}
