package scheduler

import (
	"context"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/observability"
)

// CrashReporter reports recovered panics (optional; wired to observability.Reporter).
type CrashReporter interface {
	CapturePanic(ctx context.Context, recovered any, stack []byte, tags map[string]string)
}

// Duty is cluster-wide work that runs only on the scheduler leader; ctx ends when leadership is lost or
// the node stops. Duties must be safe to restart on another node at any point.
type Duty func(ctx context.Context)

// Scheduler collects duties from other modules and runs them while this node leads.
type Scheduler struct {
	mu     sync.Mutex
	duties map[string]Duty
	leader bool
	// running duties of the current term
	cancel context.CancelFunc
	wg     sync.WaitGroup
	lctx   context.Context
	log    *slog.Logger
	crash  CrashReporter
}

// Add registers a duty. Duties added while leading start at once.
func (s *Scheduler) Add(name string, d Duty) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.duties == nil {
		s.duties = map[string]Duty{}
	}
	s.duties[name] = d
	if s.leader && s.lctx != nil {
		s.start(name, d)
	}
}

// Leader reports whether this node currently leads.
func (s *Scheduler) Leader() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.leader
}

func (s *Scheduler) start(name string, d Duty) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				stack := debug.Stack()
				s.log.Error("scheduler duty panicked; it restarts with the next leadership term", "duty", name, "panic", r)
				if s.crash != nil {
					s.crash.CapturePanic(s.lctx, r, stack, map[string]string{"component": "scheduler", "duty": name})
				}
			}
		}()
		d(s.lctx)
	}()
}

func (s *Scheduler) lead(ctx context.Context) {
	s.mu.Lock()
	s.leader = true
	s.lctx, s.cancel = context.WithCancel(ctx)
	for name, d := range s.duties {
		s.start(name, d)
	}
	s.mu.Unlock()
	<-ctx.Done()
	s.mu.Lock()
	s.leader = false
	s.cancel()
	s.mu.Unlock()
	s.wg.Wait()
}

// Module elects the scheduler leader among nodes with the scheduler role and provides *Scheduler on every
// node (modules register duties unconditionally; only the leader runs them).
type Module struct {
	TTL time.Duration // EB_LEADER_TTL
	Log *slog.Logger

	sched  *Scheduler
	cancel context.CancelFunc
	done   chan struct{}
}

// Name implements app.Module.
func (m *Module) Name() string { return "scheduler" }

// Roles implements app.Module (every node provides the registry; the election runs on scheduler nodes).
func (m *Module) Roles() []app.Role { return nil }

// Start implements app.Module.
func (m *Module) Start(ctx context.Context, a *app.App) error {
	log := m.Log
	if log == nil {
		log = a.Log
	}
	m.sched = &Scheduler{log: log}
	if rep, ok := app.Get[*observability.Reporter](a); ok {
		m.sched.crash = rep
	}
	app.Provide(a, m.sched)
	if !a.Roles.Has(app.RoleScheduler) {
		return nil
	}
	b, err := app.MustGet[*bus.Bus](a)
	if err != nil {
		return err
	}
	ectx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	m.cancel = cancel
	m.done = make(chan struct{})
	e := &Elector{Leases: b, NodeID: a.NodeID, TTL: m.TTL, Log: log, Lead: m.sched.lead,
		OnChange: func(leader bool) { log.Info("scheduler leadership changed", "leader", leader) }}
	go func() { defer close(m.done); e.Run(ectx) }()
	return nil
}

// Stop implements app.Module: stops the duties and releases leadership.
func (m *Module) Stop(context.Context) error {
	if m.cancel != nil {
		m.cancel()
		<-m.done
	}
	return nil
}
