package notify

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// defaultEngine is the engine of the running module, for call sites that are wired before the module
// starts (the HTTP error reporter in cmd/emailbridge/serve.go).
var defaultEngine atomic.Pointer[Engine]

// Default returns the engine started by Module.Start, or nil before it starts and after it stops. Every
// Engine method is nil-safe, so `notify.Default().Raise(...)` is always safe to call.
func Default() *Engine { return defaultEngine.Load() }

// Engine accepts Raise/Resolve from anywhere: buffered, in-memory dedup per node, then persist.
type Engine struct {
	Repo     *Repo
	Channels *Channels
	Log      *slog.Logger
	Now      func() time.Time

	ch     chan op
	stop   chan struct{}
	wg     sync.WaitGroup
	mu     sync.Mutex
	recent map[string]time.Time // scope\0key → last enqueue (in-memory dedup window)
}

type opKind int

const (
	opRaise opKind = iota
	opResolve
)

type op struct {
	kind       opKind
	p          Problem
	scope, key string
}

const (
	engineBuf     = 512
	dedupWindow   = 2 * time.Second
	flushInterval = 200 * time.Millisecond
)

// NewEngine builds an engine; call Start to drain the buffer.
func NewEngine(repo *Repo, ch *Channels, log *slog.Logger) *Engine {
	if log == nil {
		log = slog.Default()
	}
	return &Engine{
		Repo: repo, Channels: ch, Log: log, Now: time.Now,
		ch: make(chan op, engineBuf), stop: make(chan struct{}),
		recent: map[string]time.Time{},
	}
}

// Start runs the async worker until Stop.
func (e *Engine) Start() {
	e.wg.Add(1)
	go e.loop()
}

// Stop drains and exits the worker.
func (e *Engine) Stop() {
	close(e.stop)
	e.wg.Wait()
}

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now().UTC()
	}
	return time.Now().UTC()
}

// Raise enqueues a problem. Never blocks long; drops with a warn log if the buffer is full.
func (e *Engine) Raise(ctx context.Context, p Problem) {
	if e == nil {
		return
	}
	p.Scope = strings.TrimSpace(p.Scope)
	p.Key = strings.TrimSpace(p.Key)
	if p.Scope == "" || p.Key == "" || !knownCategories[p.Category] || !knownSeverities[p.Severity] {
		return
	}
	dk := p.Scope + "\x00" + p.Key
	e.mu.Lock()
	if t, ok := e.recent[dk]; ok && e.now().Sub(t) < dedupWindow {
		e.mu.Unlock()
		return
	}
	e.recent[dk] = e.now()
	e.mu.Unlock()
	select {
	case e.ch <- op{kind: opRaise, p: p}:
	default:
		e.Log.Warn("notify buffer full; drop raise", "key", p.Key, "scope", p.Scope)
	}
	_ = ctx
}

// Resolve enqueues a resolve for scope+key.
func (e *Engine) Resolve(ctx context.Context, scope, key string) {
	if e == nil {
		return
	}
	scope, key = strings.TrimSpace(scope), strings.TrimSpace(key)
	if scope == "" || key == "" {
		return
	}
	select {
	case e.ch <- op{kind: opResolve, scope: scope, key: key}:
	default:
		e.Log.Warn("notify buffer full; drop resolve", "key", key, "scope", scope)
	}
	_ = ctx
}

// RaiseSync persists immediately (tests / watchers that need the row back).
func (e *Engine) RaiseSync(ctx context.Context, p Problem) (*Notification, error) {
	return e.applyRaise(ctx, p)
}

// ResolveSync persists immediately.
func (e *Engine) ResolveSync(ctx context.Context, scope, key string) (*Notification, error) {
	return e.applyResolve(ctx, scope, key)
}

func (e *Engine) loop() {
	defer e.wg.Done()
	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-e.stop:
			for {
				select {
				case o := <-e.ch:
					e.handle(o)
				default:
					return
				}
			}
		case o := <-e.ch:
			e.handle(o)
		case <-ticker.C:
			e.mu.Lock()
			cut := e.now().Add(-dedupWindow * 2)
			for k, t := range e.recent {
				if t.Before(cut) {
					delete(e.recent, k)
				}
			}
			e.mu.Unlock()
		}
	}
}

func (e *Engine) handle(o op) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	switch o.kind {
	case opRaise:
		if _, err := e.applyRaise(ctx, o.p); err != nil {
			e.Log.Warn("notify raise failed", "err", err, "key", o.p.Key)
		}
	case opResolve:
		if _, err := e.applyResolve(ctx, o.scope, o.key); err != nil {
			e.Log.Warn("notify resolve failed", "err", err, "key", o.key)
		}
	}
}

func (e *Engine) applyRaise(ctx context.Context, p Problem) (*Notification, error) {
	n, created, severityRose, err := e.Repo.UpsertRaise(ctx, p, e.now())
	if err != nil {
		return nil, err
	}
	if e.Channels != nil {
		e.Channels.OnRaised(ctx, n, created, severityRose)
	}
	return n, nil
}

func (e *Engine) applyResolve(ctx context.Context, scope, key string) (*Notification, error) {
	n, err := e.Repo.ResolveKey(ctx, scope, key, e.now())
	if err != nil {
		return nil, err
	}
	if n != nil && e.Channels != nil {
		e.Channels.OnResolved(ctx, n)
	}
	return n, nil
}

// CaptureError raises an instance-scoped internal problem (reporter hook).
func (e *Engine) CaptureError(ctx context.Context, err error, tags map[string]string) {
	if e == nil || err == nil {
		return
	}
	fp := fingerprint(err, tags)
	comp := tags["component"]
	if comp == "" {
		comp = "unknown"
	}
	e.Raise(ctx, Problem{
		Scope: ScopeInstance, Key: "internal:" + fp, Category: CatInternal, Severity: SeverityWarning,
		TitleKey: "notify.internal.title", DetailKey: "notify.internal.detail",
		TitleParams:  map[string]string{"component": comp},
		DetailParams: map[string]string{"component": comp, "code": tags["code"]},
		Link:         "/cluster", Data: map[string]any{"component": comp, "code": tags["code"]},
		Fingerprint: fp,
	})
}

// CapturePanic raises an instance-scoped internal problem for a recovered panic (reporter hook). The panic
// value is grouped like an error, so the same panic site keeps bumping one problem.
func (e *Engine) CapturePanic(ctx context.Context, rec any, tags map[string]string) {
	if e == nil || rec == nil {
		return
	}
	err, ok := rec.(error)
	if !ok {
		err = fmt.Errorf("panic: %v", rec)
	}
	t := map[string]string{"code": "panic"}
	for k, v := range tags {
		t[k] = v
	}
	e.CaptureError(ctx, err, t)
}
