package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"sync"
	"time"
)

// Module is a unit of the application with a lifecycle. Platform modules (config, logging, stores, bus)
// return no roles and run on every node; domain modules list the roles they belong to.
type Module interface {
	// Name identifies the module in logs and errors.
	Name() string
	// Roles lists the roles the module runs in; empty means every node.
	Roles() []Role
	// Start initialises the module. It may register shared dependencies with Provide. It must return
	// once the module is running (long-running work goes into goroutines bound to ctx or Stop).
	Start(ctx context.Context, a *App) error
	// Stop releases the module's resources. It must honour ctx's deadline.
	Stop(ctx context.Context) error
}

// Checker is implemented by modules that contribute to /readyz.
type Checker interface {
	Name() string
	Ready(ctx context.Context) error
}

// BuildInfo is set at build time through -ldflags.
type BuildInfo struct {
	Version string
	Commit  string
	Date    string
}

// App holds the node's identity, its modules and the shared dependencies modules provide to each other.
type App struct {
	Roles   RoleSet
	NodeID  string
	Build   BuildInfo
	Log     *slog.Logger
	Grace   time.Duration
	modules []Module

	mu      sync.Mutex
	started []Module
	deps    map[reflect.Type]any
}

// Options configure New.
type Options struct {
	Roles   RoleSet
	NodeID  string
	Build   BuildInfo
	Log     *slog.Logger
	Grace   time.Duration
	Modules []Module
}

// New creates an App. Modules start in the given order and stop in reverse order.
func New(o Options) *App {
	log := o.Log
	if log == nil {
		log = slog.Default()
	}
	grace := o.Grace
	if grace <= 0 {
		grace = 60 * time.Second
	}
	return &App{
		Roles:   o.Roles,
		NodeID:  o.NodeID,
		Build:   o.Build,
		Log:     log,
		Grace:   grace,
		modules: o.Modules,
		deps:    map[reflect.Type]any{},
	}
}

// Provide registers a shared dependency by its static type T, e.g. Provide[*store.Client](a, c).
// Registering the same type twice is a programming error and panics at startup.
func Provide[T any](a *App, v T) {
	a.mu.Lock()
	defer a.mu.Unlock()
	t := reflect.TypeFor[T]()
	if _, ok := a.deps[t]; ok {
		panic(fmt.Sprintf("app: dependency %v provided twice", t))
	}
	a.deps[t] = v
}

// Get returns the dependency of type T and whether it was provided.
func Get[T any](a *App) (T, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	v, ok := a.deps[reflect.TypeFor[T]()]
	if !ok {
		var zero T
		return zero, false
	}
	return v.(T), true
}

// MustGet returns the dependency of type T or an error naming the missing type; modules call it in Start
// so a wiring mistake fails at startup, never at request time.
func MustGet[T any](a *App) (T, error) {
	v, ok := Get[T](a)
	if !ok {
		return v, fmt.Errorf("app: dependency %v not provided (module order in modules.go?)", reflect.TypeFor[T]())
	}
	return v, nil
}

// Checkers returns the started modules that report readiness.
func (a *App) Checkers() []Checker {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []Checker
	for _, m := range a.started {
		if c, ok := m.(Checker); ok {
			out = append(out, c)
		}
	}
	return out
}

// Start starts every module whose roles intersect the node's roles. On the first failure it stops the
// modules already started (in reverse) and returns the error.
func (a *App) Start(ctx context.Context) error {
	for _, m := range a.modules {
		if !a.Roles.Any(m.Roles()) {
			continue
		}
		t0 := time.Now()
		if err := m.Start(ctx, a); err != nil {
			stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), a.Grace)
			defer cancel()
			stopErr := a.Stop(stopCtx)
			return errors.Join(fmt.Errorf("app: start %s: %w", m.Name(), err), stopErr)
		}
		a.mu.Lock()
		a.started = append(a.started, m)
		a.mu.Unlock()
		a.Log.Debug("module started", "module", m.Name(), "took", time.Since(t0))
	}
	a.Log.Info("node started", "node", a.NodeID, "roles", a.Roles.String(), "version", a.Build.Version)
	return nil
}

// Stop stops the started modules in reverse order. Every module gets the remaining time of ctx; errors
// are collected, never short-circuited, so one failing module cannot leave others running.
func (a *App) Stop(ctx context.Context) error {
	a.mu.Lock()
	started := a.started
	a.started = nil
	a.mu.Unlock()
	var errs []error
	for i := len(started) - 1; i >= 0; i-- {
		m := started[i]
		if err := m.Stop(ctx); err != nil {
			errs = append(errs, fmt.Errorf("app: stop %s: %w", m.Name(), err))
		}
	}
	return errors.Join(errs...)
}

// Run starts the app, blocks until ctx is cancelled (SIGTERM/SIGINT), then stops it within Grace.
func (a *App) Run(ctx context.Context) error {
	if err := a.Start(ctx); err != nil {
		return err
	}
	<-ctx.Done()
	a.Log.Info("shutdown requested", "grace", a.Grace)
	stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), a.Grace)
	defer cancel()
	err := a.Stop(stopCtx)
	if err != nil {
		a.Log.Error("shutdown finished with errors", "err", err)
	} else {
		a.Log.Info("shutdown complete")
	}
	return err
}
