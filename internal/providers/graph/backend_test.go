package graph_test

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/events"
	"github.com/radiobros/emailbridge/internal/events/types"
	"github.com/radiobros/emailbridge/internal/providers/graph"
	"github.com/radiobros/emailbridge/internal/store"
	ebsync "github.com/radiobros/emailbridge/internal/sync"
	simgraph "github.com/radiobros/emailbridge/simulator/graph"
)

// stateLog collects state transitions written by the backend goroutine; the test goroutine reads them.
type stateLog struct {
	mu     sync.Mutex
	states []string
}

func (l *stateLog) add(s string) {
	l.mu.Lock()
	l.states = append(l.states, s)
	l.mu.Unlock()
}

func (l *stateLog) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.states...)
}

func (l *stateLog) has(s string) bool {
	for _, x := range l.all() {
		if x == s {
			return true
		}
	}
	return false
}

func (l *stateLog) last() string {
	all := l.all()
	if len(all) == 0 {
		return ""
	}
	return all[len(all)-1]
}

func TestBackend_runEmitsReceived(t *testing.T) {
	sim := simgraph.New()
	defer sim.Close()
	sim.SetToken("u@ex.com", "tok")

	sink := &graph.MemorySink{}
	var drafts []events.Draft
	b := &graph.Backend{
		Client: &graph.Client{BaseURL: sim.URL()},
		Config: graph.Config{PullInterval: 50 * time.Millisecond},
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	acc := &ebsync.Account{
		ID: store.NewID(store.PrefixAccount), OrganizationID: store.NewID(store.PrefixOrganization),
		Email: "u@ex.com", Provider: ebsync.ProviderGraph, Paths: []string{"INBOX"},
		NotifyFrom: time.Now().Add(-time.Hour),
		Token:      func(context.Context) (string, string, error) { return "tok", "u@ex.com", nil },
	}
	cmds := make(chan ebsync.Command)
	states := &stateLog{}
	env := ebsync.BackendEnv{
		Write: func(_ context.Context, d []events.Draft, fn func(context.Context) error) error {
			drafts = append(drafts, d...)
			for _, x := range d {
				_ = sink.Emit(context.Background(), acc.OrganizationID, acc.ID, x.Type, x.Subject, x.Data)
			}
			if fn != nil {
				return fn(context.Background())
			}
			return nil
		},
		Draft: func(typ, subject string, data any) events.Draft {
			return events.Draft{Type: typ, OrganizationID: acc.OrganizationID, AccountID: acc.ID, Subject: subject, Data: data}
		},
		Transition: func(_ context.Context, to string, _ *types.Reason) { states.add(to) },
		Needs:      ebsync.NoNeeds{},
		Commands:   cmds,
		Log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:        func() time.Time { return time.Now().UTC() },
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- b.Run(ctx, acc, env) }()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if states.last() == ebsync.StateConnected {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	sim.PutMessage("u@ex.com", rawMsg(), "inbox")
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(sink.ReceivedOf()) >= 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil && ctx.Err() == nil {
		t.Log(err)
	}
	if len(sink.ReceivedOf()) < 1 {
		t.Fatalf("want received, drafts=%d states=%v", len(drafts), states.all())
	}
	got := sink.ReceivedOf()[0]
	imap := graph.IMAPShapedReceived(graph.Account{ID: acc.ID, OrganizationID: acc.OrganizationID},
		got.Mailbox.MailboxID, "INBOX", `\Inbox`, got.UID, got.UIDValidity, rawMsg())
	if got.Subject != imap.Subject || got.MessageIDHeader != imap.MessageIDHeader {
		t.Fatalf("parity %q/%q vs %q/%q", got.Subject, got.MessageIDHeader, imap.Subject, imap.MessageIDHeader)
	}
	if got.EmailID == "" || got.ThreadID == "" {
		t.Fatal("provider ids")
	}
}

func TestTokenURLForTenant(t *testing.T) {
	if u := graph.TokenURLForTenant("tid", ""); u != "https://login.microsoftonline.com/tid/oauth2/v2.0/token" {
		t.Fatal(u)
	}
	if u := graph.TokenURLForTenant("tid", "http://sim/token"); u != "http://sim/token" {
		t.Fatal(u)
	}
}

func TestValidateGraphHelpers(t *testing.T) {
	_ = events.MessageReceived
}

func TestBackend_nilClientConnectError(t *testing.T) {
	b := &graph.Backend{}
	states := &stateLog{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- b.Run(ctx, &ebsync.Account{ID: "a", Email: "u@ex.com"}, ebsync.BackendEnv{
			Transition: func(_ context.Context, to string, _ *types.Reason) { states.add(to) },
		})
	}()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if all := states.all(); len(all) > 0 && all[0] == ebsync.StateConnectError {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil && ctx.Err() == nil {
		t.Log(err)
	}
	if all := states.all(); len(all) == 0 || all[0] != ebsync.StateConnectError {
		t.Fatalf("states=%v", all)
	}
}

func TestBackend_authFailAndCommands(t *testing.T) {
	sim := simgraph.New()
	defer sim.Close()
	sim.SetToken("u@ex.com", "tok")

	b := &graph.Backend{
		Client: &graph.Client{BaseURL: sim.URL()},
		Config: graph.Config{PullInterval: time.Hour},
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	acc := &ebsync.Account{
		ID: store.NewID(store.PrefixAccount), OrganizationID: store.NewID(store.PrefixOrganization),
		Email: "u@ex.com", Provider: ebsync.ProviderGraph, Paths: []string{"INBOX"},
		NotifyFrom: time.Now().Add(-time.Hour),
	}
	// The token is revoked until the test "reconnects" the account; the backend reads it concurrently.
	var tokenOK atomic.Bool
	acc.Token = func(context.Context) (string, string, error) {
		if tokenOK.Load() {
			return "tok", "u@ex.com", nil
		}
		return "", "", errs.New(errs.CodeOauthRevoked, "revoked")
	}
	cmds := make(chan ebsync.Command, 2)
	states := &stateLog{}
	env := ebsync.BackendEnv{
		Write: func(_ context.Context, _ []events.Draft, fn func(context.Context) error) error {
			if fn != nil {
				return fn(context.Background())
			}
			return nil
		},
		Draft: func(typ, subject string, data any) events.Draft {
			return events.Draft{Type: typ, Subject: subject, Data: data}
		},
		Transition: func(_ context.Context, to string, _ *types.Reason) { states.add(to) },
		Needs:      ebsync.NoNeeds{},
		Commands:   cmds,
		Log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- b.Run(ctx, acc, env) }()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if states.has(ebsync.StateAuthError) {
			goto authOK
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatalf("want authError states=%v", states.all())
authOK:
	tokenOK.Store(true)
	cmds <- ebsync.Command{Cmd: ebsync.CmdReconnect}
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if states.has(ebsync.StateConnected) {
			goto connected
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatalf("want connected after reconnect states=%v", states.all())
connected:
	cmds <- ebsync.Command{Cmd: ebsync.CmdResync}
	time.Sleep(200 * time.Millisecond)
	cancel()
	if err := <-done; err != nil && ctx.Err() == nil {
		t.Log(err)
	}
}

func TestBackend_nilTokenAuthError(t *testing.T) {
	sim := simgraph.New()
	defer sim.Close()
	bad := &graph.Backend{Client: &graph.Client{BaseURL: sim.URL()}, Config: graph.Config{PullInterval: time.Hour}}
	acc2 := &ebsync.Account{ID: "a", Email: "u@ex.com"} // Token nil → oauth_not_connected
	states := &stateLog{}
	ctx2, cancel2 := context.WithCancel(context.Background())
	done2 := make(chan error, 1)
	go func() {
		done2 <- bad.Run(ctx2, acc2, ebsync.BackendEnv{
			Transition: func(_ context.Context, to string, _ *types.Reason) { states.add(to) },
			Commands:   make(chan ebsync.Command),
			Log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		})
	}()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if states.last() == ebsync.StateAuthError {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	cancel2()
	if err := <-done2; err != nil && ctx2.Err() == nil {
		t.Log(err)
	}
	if last := states.last(); last != ebsync.StateAuthError {
		t.Fatalf("last=%s", last)
	}
}

func TestModule_NewBackend(t *testing.T) {
	m := &graph.Module{BaseURL: "http://127.0.0.1:9", Log: slog.Default()}
	a := app.New(app.Options{Log: slog.Default(), Roles: app.RoleSet{app.RoleSync: true}, NodeID: "n1"})
	if err := m.Start(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	b := m.NewBackend()
	if b == nil || b.Client == nil {
		t.Fatal("NewBackend")
	}
}

func TestStateRepo_mongoRoundtrip(t *testing.T) {
	uri := "mongodb://127.0.0.1:27017/?replicaSet=rs0&directConnection=true"
	ctx := context.Background()
	db, err := store.Connect(ctx, store.Options{URI: uri, Database: "eb_graph_state_" + store.NewID(store.PrefixOrganization), ConnectTimeout: 3 * time.Second})
	if err != nil {
		t.Skipf("mongo: %v", err)
	}
	t.Cleanup(func() { _ = db.DB.Drop(ctx); _ = db.Close(ctx) })
	if err := graph.Migration(35).Up(ctx, db); err != nil {
		t.Fatal(err)
	}
	repo := &graph.StateRepo{DB: db}
	acc := &graph.Account{ID: store.NewID(store.PrefixAccount), OrganizationID: store.NewID(store.PrefixOrganization)}
	st, err := repo.Load(ctx, acc)
	if err != nil || st.UIDValidity == 0 {
		t.Fatalf("%v %+v", err, st)
	}
	st.ClientState = "cs"
	st.DeltaLinks["INBOX"] = "http://delta"
	if err := repo.Save(ctx, st); err != nil {
		t.Fatal(err)
	}
	st2, err := repo.Load(ctx, acc)
	if err != nil || st2.ClientState != "cs" || st2.DeltaLinks["INBOX"] == "" {
		t.Fatalf("%v %+v", err, st2)
	}
}
