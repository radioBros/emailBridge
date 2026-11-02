package observability

import (
	"context"
	"sync"
	"time"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/logging"
	"github.com/radiobros/emailbridge/internal/store"
)

// ModuleOptions configure the observability platform module.
type ModuleOptions struct {
	MetricsAddr string
	Tracing     TracingOptions
	Reporter    ReporterOptions
	LogSystem   *logging.System
}

// Module starts metrics and tracing, provides *Metrics and *Reporter.
type Module struct {
	Options ModuleOptions

	metrics   *Metrics
	reporter  *Reporter
	shutdown  func(context.Context) error
	cancel    context.CancelFunc
	serveDone chan struct{}
	collectWG sync.WaitGroup
}

// Name implements app.Module.
func (m *Module) Name() string { return "observability" }

// Roles implements app.Module (platform).
func (m *Module) Roles() []app.Role { return nil }

// Start implements app.Module.
func (m *Module) Start(ctx context.Context, a *app.App) error {
	metrics := NewMetrics()
	if m.Options.LogSystem != nil {
		if err := metrics.ObserveLogs(m.Options.LogSystem); err != nil {
			return err
		}
	}
	if db, ok := app.Get[*store.DB](a); ok {
		metrics.ObserveMongo(MongoCollector(db))
	}
	rep, err := NewReporter(m.Options.Reporter)
	if err != nil {
		return err
	}
	shutdown, err := InitTracing(ctx, m.Options.Tracing)
	if err != nil {
		return err
	}
	m.metrics = metrics
	m.reporter = rep
	m.shutdown = shutdown
	app.Provide(a, metrics)
	app.Provide(a, rep)

	sctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	m.cancel = cancel
	m.collectWG.Add(1)
	go func() {
		defer m.collectWG.Done()
		metrics.startCollectors(sctx)
	}()
	if m.Options.MetricsAddr != "" {
		m.serveDone = make(chan struct{})
		go func() {
			defer close(m.serveDone)
			_ = metrics.Serve(sctx, m.Options.MetricsAddr)
		}()
	}
	return nil
}

// Stop flushes the reporter, stops metrics and shuts down tracing.
func (m *Module) Stop(ctx context.Context) error {
	if m.cancel != nil {
		m.cancel()
		m.collectWG.Wait()
		if m.serveDone != nil {
			select {
			case <-m.serveDone:
			case <-ctx.Done():
			}
		}
	}
	if m.reporter != nil {
		m.reporter.Flush(2 * time.Second)
	}
	if m.shutdown != nil {
		return m.shutdown(ctx)
	}
	return nil
}
