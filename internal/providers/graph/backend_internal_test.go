package graph

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/events/types"
	ebsync "github.com/radiobros/emailbridge/internal/sync"
)

func TestWake_andFail(t *testing.T) {
	r := &Runner{}
	r.Wake() // nil channel
	r.wake = make(chan struct{}, 1)
	r.Wake()
	r.Wake() // default branch when full

	b := &Backend{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	var got string
	var reason *types.Reason
	env := ebsync.BackendEnv{
		Transition: func(_ context.Context, to string, r *types.Reason) { got = to; reason = r },
		Log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	b.fail(context.Background(), env, nil)
	if got != "" {
		t.Fatal("nil err should no-op")
	}
	b.fail(context.Background(), env, ErrSyncStateNotFound)
	if got != ebsync.StateConnectError || reason == nil || reason.Code != "sync_state_not_found" {
		t.Fatalf("%s %+v", got, reason)
	}
	b.fail(context.Background(), env, errs.New(errs.CodeMailAuthFailed, "bad"))
	if got != ebsync.StateAuthError {
		t.Fatal(got)
	}
	b.fail(context.Background(), env, errs.New(errs.CodeOauthNotConnected, "x"))
	if got != ebsync.StateAuthError {
		t.Fatal(got)
	}
	b.fail(context.Background(), env, errors.New("temp"))
	if got != ebsync.StateConnectError {
		t.Fatal(got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	b.fail(ctx, env, errors.New("ignored"))
}

func TestForceFull(t *testing.T) {
	r := &Runner{
		Client: &Client{BaseURL: "http://127.0.0.1:9"},
		Tokens: StaticToken{Token: "t"},
		Config: Config{PullInterval: time.Hour},
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	acc := Account{ID: "a", OrganizationID: "o", Email: "u@ex.com", UPN: "u@ex.com"}
	r.ensureState(acc)
	r.State.DeltaLinks["INBOX"] = "http://old"
	err := r.forceFull(context.Background(), acc)
	if err == nil {
		t.Fatal("want dial err")
	}
	if len(r.State.DeltaLinks) != 0 {
		t.Fatalf("delta cleared: %v", r.State.DeltaLinks)
	}
}

func TestTruncateOrEmpty(t *testing.T) {
	if orEmpty(nil) == nil || len(orEmpty([]string{"a"})) != 1 {
		t.Fatal("orEmpty")
	}
	if truncateStr("hi", 0) != "hi" || truncateStr("abcdef", 3) != "abc" {
		t.Fatal("truncate")
	}
}
