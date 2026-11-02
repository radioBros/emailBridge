package sysmail

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/mailproto/smtpc"
)

func TestModuleLifecycle(t *testing.T) {
	b, err := bus.Open(context.Background(), bus.Options{Mode: bus.ModeEmbedded, DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)
	a := app.New(app.Options{NodeID: "n1", Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Modules: nil})
	app.Provide(a, b)
	m := &Module{Options: Options{Mode: ModeSMTP, From: "a@b", Env: "production", Log: slog.Default()}}
	if m.Name() != "sysmail" || m.Roles() != nil {
		t.Fatal("meta")
	}
	if err := m.Start(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if _, ok := app.Get[*Service](a); !ok {
		t.Fatal("service not provided")
	}
	if err := m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestModuleStartOffNoWorker(t *testing.T) {
	a := app.New(app.Options{NodeID: "n1", Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	m := &Module{Options: Options{Mode: ModeOff}}
	if err := m.Start(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	_ = m.Stop(context.Background())
}

func TestEnqueueValidation(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := New(Options{Mode: ModeSMTP, Log: log}, &memPub{})
	if err := s.Enqueue(context.Background(), Mail{Kind: "nope", To: "a@b"}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	if err := s.Enqueue(context.Background(), Mail{Kind: KindAlert, To: ""}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	if err := (*Service)(nil).Enqueue(context.Background(), Mail{}); errs.CodeOf(err) != errs.CodeInternalError {
		t.Fatal(err)
	}
	s2 := New(Options{Mode: "weird", Log: log}, &memPub{})
	if err := s2.Enqueue(context.Background(), Mail{Kind: KindAlert, To: "a@b"}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	s3 := New(Options{Mode: ModeSMTP, Log: log}, nil)
	if err := s3.Enqueue(context.Background(), Mail{Kind: KindAlert, To: "a@b"}); errs.CodeOf(err) != errs.CodeInternalError {
		t.Fatal(err)
	}
}

func TestHandleJobBranches(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := New(Options{Mode: ModeSMTP, From: "a@b", Env: "production", Log: log}, &memPub{})

	if err := s.HandleJob(context.Background(), []byte(`{`)); err != nil {
		t.Fatal(err)
	}

	s.send = func(context.Context, smtpc.Options, string, []string, []byte) error {
		return errs.New(errs.CodeSMTPPermanent, "550")
	}
	j, _ := json.Marshal(job{ID: "1", Kind: KindAlert, To: "x@y.com", Data: map[string]string{"Title": "t", "Body": "b"}})
	if err := s.HandleJob(context.Background(), j); err != nil {
		t.Fatal("permanent must ack", err)
	}

	s.send = nil
	if err := s.HandleJob(context.Background(), j); err != nil {
		t.Fatal(err)
	}

	// rate limit: fill bucket then drop
	s2 := New(Options{Mode: ModeSMTP, From: "a@b", Env: "production", Log: log}, &memPub{})
	s2.send = func(context.Context, smtpc.Options, string, []string, []byte) error { return nil }
	now := time.Now()
	for i := 0; i < rateLimitPerHour; i++ {
		if !s2.rate.allow("rate@x.com", now) {
			t.Fatal(i)
		}
	}
	j2, _ := json.Marshal(job{ID: "2", Kind: KindAlert, To: "rate@x.com", Data: map[string]string{"Title": "t", "Body": "b"}})
	if err := s2.HandleJob(context.Background(), j2); err != nil {
		t.Fatal(err)
	}
}

func TestRenderAlertTitleSubject(t *testing.T) {
	r, err := Render(KindAlert, "en", "noreply@example.com", "test@example.com", map[string]string{"Title": "Custom", "Body": "x"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Subject != "Custom" {
		t.Fatal(r.Subject)
	}
}
