package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

type recorder struct {
	mu  sync.Mutex
	log []string
}

func (r *recorder) add(s string) {
	r.mu.Lock()
	r.log = append(r.log, s)
	r.mu.Unlock()
}

func (r *recorder) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.log, ",")
}

type fakeModule struct {
	name     string
	roles    []Role
	rec      *recorder
	startErr error
	stopErr  error
	stopWait time.Duration
	ready    error
}

func (m *fakeModule) Name() string  { return m.name }
func (m *fakeModule) Roles() []Role { return m.roles }
func (m *fakeModule) Start(_ context.Context, _ *App) error {
	m.rec.add("start:" + m.name)
	return m.startErr
}
func (m *fakeModule) Stop(ctx context.Context) error {
	if m.stopWait > 0 {
		select {
		case <-time.After(m.stopWait):
		case <-ctx.Done():
			m.rec.add("stop-timeout:" + m.name)
			return ctx.Err()
		}
	}
	m.rec.add("stop:" + m.name)
	return m.stopErr
}

type readyModule struct{ fakeModule }

func (m *readyModule) Ready(context.Context) error { return m.ready }

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestParseRoles(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr string
	}{
		{"", "api,sync,submit,webhook,scheduler", ""},
		{"all", "api,sync,submit,webhook,scheduler", ""},
		{" ALL ", "api,sync,submit,webhook,scheduler", ""},
		{"api", "api", ""},
		{"webhook, api", "api,webhook", ""},
		{"sync,sync", "sync", ""},
		{"api,", "api", ""},
		{"api,all", "", "cannot be combined"},
		{"mailer", "", "unknown role"},
		{",", "", "no role given"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseRoles(tt.in)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("ParseRoles(%q) error = %v, want containing %q", tt.in, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseRoles(%q) unexpected error: %v", tt.in, err)
			}
			if got.String() != tt.want {
				t.Fatalf("ParseRoles(%q) = %q, want %q", tt.in, got.String(), tt.want)
			}
		})
	}
}

func TestRoleSetAny(t *testing.T) {
	s, _ := ParseRoles("api,webhook")
	if !s.Any(nil) {
		t.Error("empty role list must match every set")
	}
	if !s.Any([]Role{RoleSync, RoleWebhook}) {
		t.Error("expected match on webhook")
	}
	if s.Any([]Role{RoleSync, RoleSubmit}) {
		t.Error("unexpected match")
	}
	if !s.Has(RoleAPI) || s.Has(RoleSync) {
		t.Error("Has wrong")
	}
}

func TestStartStopOrderAndRoleFiltering(t *testing.T) {
	rec := &recorder{}
	roles, _ := ParseRoles("api")
	a := New(Options{Roles: roles, Log: quiet(), Modules: []Module{
		&fakeModule{name: "config", rec: rec},
		&fakeModule{name: "sync", roles: []Role{RoleSync}, rec: rec},
		&fakeModule{name: "http", roles: []Role{RoleAPI}, rec: rec},
	}})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error)
	go func() { done <- a.Run(ctx) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got, want := rec.String(), "start:config,start:http,stop:http,stop:config"; got != want {
		t.Fatalf("lifecycle = %s, want %s", got, want)
	}
}

func TestStartFailureStopsStartedModules(t *testing.T) {
	rec := &recorder{}
	a := New(Options{Roles: RoleSet{RoleAPI: true}, Log: quiet(), Modules: []Module{
		&fakeModule{name: "a", rec: rec},
		&fakeModule{name: "b", rec: rec, startErr: errors.New("boom")},
		&fakeModule{name: "c", rec: rec},
	}})
	err := a.Start(context.Background())
	if err == nil || !strings.Contains(err.Error(), "start b: boom") {
		t.Fatalf("Start error = %v", err)
	}
	if got, want := rec.String(), "start:a,start:b,stop:a"; got != want {
		t.Fatalf("lifecycle = %s, want %s", got, want)
	}
}

func TestStopCollectsErrorsAndHonoursGrace(t *testing.T) {
	rec := &recorder{}
	a := New(Options{Roles: RoleSet{RoleAPI: true}, Log: quiet(), Grace: 50 * time.Millisecond, Modules: []Module{
		&fakeModule{name: "a", rec: rec, stopErr: errors.New("a failed")},
		&fakeModule{name: "slow", rec: rec, stopWait: time.Second},
		&fakeModule{name: "c", rec: rec},
	}})
	if err := a.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), a.Grace)
	defer cancel()
	start := time.Now()
	err := a.Stop(ctx)
	if time.Since(start) > 500*time.Millisecond {
		t.Fatal("Stop did not honour the deadline")
	}
	if err == nil || !strings.Contains(err.Error(), "stop a: a failed") || !strings.Contains(err.Error(), "stop slow") {
		t.Fatalf("Stop error = %v", err)
	}
	if got := rec.String(); !strings.HasSuffix(got, "stop:c,stop-timeout:slow,stop:a") {
		t.Fatalf("stop order = %s", got)
	}
	if err := a.Stop(context.Background()); err != nil {
		t.Fatalf("second Stop must be a no-op, got %v", err)
	}
}

func TestRegistry(t *testing.T) {
	a := New(Options{})
	type store struct{ name string }
	if _, ok := Get[*store](a); ok {
		t.Fatal("unexpected dependency")
	}
	if _, err := MustGet[*store](a); err == nil || !strings.Contains(err.Error(), "not provided") {
		t.Fatalf("MustGet error = %v", err)
	}
	Provide(a, &store{name: "mongo"})
	got, err := MustGet[*store](a)
	if err != nil || got.name != "mongo" {
		t.Fatalf("MustGet = %v, %v", got, err)
	}
	defer func() {
		if recover() == nil {
			t.Fatal("providing twice must panic")
		}
	}()
	Provide(a, &store{})
}

func TestCheckersOnlyStartedReadyModules(t *testing.T) {
	rec := &recorder{}
	r := &readyModule{fakeModule{name: "db", rec: rec}}
	a := New(Options{Roles: RoleSet{RoleAPI: true}, Log: quiet(), Modules: []Module{
		&fakeModule{name: "plain", rec: rec},
		r,
		&readyModule{fakeModule{name: "syncdb", roles: []Role{RoleSync}, rec: rec}},
	}})
	if len(a.Checkers()) != 0 {
		t.Fatal("no checkers before start")
	}
	if err := a.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	cs := a.Checkers()
	if len(cs) != 1 || cs[0].Name() != "db" {
		t.Fatalf("checkers = %v", cs)
	}
}

func TestNewDefaults(t *testing.T) {
	a := New(Options{})
	if a.Grace != 60*time.Second || a.Log == nil {
		t.Fatalf("defaults: grace=%v log=%v", a.Grace, a.Log)
	}
}
