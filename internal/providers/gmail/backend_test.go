package gmail

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/events"
	"github.com/radiobros/emailbridge/internal/events/types"
	"github.com/radiobros/emailbridge/internal/store"
	ebsync "github.com/radiobros/emailbridge/internal/sync"
	simgmail "github.com/radiobros/emailbridge/simulator/gmail"
)

func TestBackend_RunSyncOnceAndCommands(t *testing.T) {
	sim := simgmail.New()
	defer sim.Close()
	sim.SetToken("g@ex.com", "tok")
	b := &Backend{Client: &Client{BaseURL: sim.URL()}, Config: Config{PullInterval: 150 * time.Millisecond}, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	acc := &ebsync.Account{
		ID: store.NewID(store.PrefixAccount), OrganizationID: store.NewID(store.PrefixOrganization),
		Email: "g@ex.com", Provider: ebsync.ProviderGmail, Paths: []string{"*"},
		Token: func(context.Context) (string, string, error) { return "tok", "g@ex.com", nil },
	}
	cmds := make(chan ebsync.Command, 2)
	var states []string
	sink := &MemorySink{}
	env := ebsync.BackendEnv{
		Epoch: 1,
		Write: func(ctx context.Context, drafts []events.Draft, fn func(context.Context) error) error {
			if fn != nil {
				if err := fn(ctx); err != nil {
					return err
				}
			}
			for _, d := range drafts {
				_ = sink.Emit(ctx, acc.OrganizationID, acc.ID, d.Type, d.Subject, d.Data)
			}
			return nil
		},
		Draft: func(typ, subject string, data any) events.Draft {
			return events.Draft{Type: typ, OrganizationID: acc.OrganizationID, AccountID: acc.ID, Subject: subject, Data: data}
		},
		Transition: func(_ context.Context, to string, _ *types.Reason) { states = append(states, to) },
		Needs:      ebsync.NoNeeds{},
		Commands:   cmds,
		Log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:        func() time.Time { return time.Now().UTC() },
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- b.Run(ctx, acc, env) }()
	time.Sleep(400 * time.Millisecond) // initial full sync
	sim.PutMessage("g@ex.com", []byte("From: a@ex.com\r\nTo: b@ex.com\r\nSubject: Hi\r\nMessage-ID: <h@e>\r\n\r\nx"),
		[]string{"INBOX", "UNREAD"})
	cmds <- ebsync.Command{Cmd: ebsync.CmdReconnect} // history sync, not silent full
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if len(sink.ReceivedOf()) >= 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil && ctx.Err() == nil {
		t.Fatalf("Run: %v", err)
	}
	if len(states) == 0 {
		t.Fatal("no transitions")
	}
	if len(sink.ReceivedOf()) < 1 {
		t.Fatal("expected received via backend")
	}
}

func TestFolderPaths_userLabel(t *testing.T) {
	paths := FolderPaths([]string{"INBOX", "Label_1"}, map[string]string{"Label_1": "Work"}, []string{"*"})
	var haveUser bool
	for _, p := range paths {
		if p.Path == "Label/Work" {
			haveUser = true
		}
	}
	if !haveUser {
		t.Fatalf("%+v", paths)
	}
	if UserLabelPath("") != "" || UserLabelPath("  x  ") != "Label/x" {
		t.Fatal(UserLabelPath("  x  "))
	}
	_ = orEmpty(nil)
	_ = truncateStr("abcdef", 3)
}

func TestBackend_failAndForceFull(t *testing.T) {
	sim := simgmail.New()
	defer sim.Close()
	sim.SetToken("g@ex.com", "tok")
	b := &Backend{Client: &Client{BaseURL: sim.URL()}, Config: Config{PullInterval: time.Hour}}
	var to string
	env := ebsync.BackendEnv{
		Transition: func(_ context.Context, s string, r *types.Reason) { to = s; _ = r },
		Log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	b.fail(context.Background(), env, errs.New(errs.CodeOauthRevoked, "revoked"))
	if to != ebsync.StateAuthError {
		t.Fatalf("state %s", to)
	}
	b.fail(context.Background(), env, ErrHistoryExpired)
	if to != ebsync.StateConnectError {
		t.Fatalf("state %s", to)
	}

	acc := &Account{ID: store.NewID(store.PrefixAccount), OrganizationID: store.NewID(store.PrefixOrganization), Email: "g@ex.com", Paths: []string{"*"}}
	r := &Runner{Client: &Client{BaseURL: sim.URL()}, Tokens: tok{"tok", "g@ex.com"}, State: &MemoryState{}, Sink: &MemorySink{}}
	if err := r.SyncOnce(context.Background(), acc); err != nil {
		t.Fatal(err)
	}
	sim.PutMessage("g@ex.com", []byte("From: a@ex.com\r\nTo: b@ex.com\r\nSubject: F\r\nMessage-ID: <f@e>\r\n\r\nx"), []string{"INBOX"})
	if err := r.forceFull(context.Background(), acc); err != nil {
		t.Fatal(err)
	}
}

func TestBackend_nilClient(t *testing.T) {
	b := &Backend{}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	var to string
	err := b.Run(ctx, &ebsync.Account{ID: "acc_x"}, ebsync.BackendEnv{
		Transition: func(_ context.Context, s string, _ *types.Reason) { to = s },
		Commands:   make(chan ebsync.Command),
	})
	if err == nil && to != ebsync.StateConnectError {
		t.Fatalf("%v %s", err, to)
	}
}
