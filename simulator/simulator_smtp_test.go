package simulator_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/mailproto/imapc"
	"github.com/radiobros/emailbridge/internal/mailproto/smtpc"
	"github.com/radiobros/emailbridge/simulator"
	"github.com/radiobros/emailbridge/simulator/simtest"
)

func openSMTP(t *testing.T, env *simtest.Env, user, pass string, mode smtpc.TLSMode) *smtpc.Session {
	t.Helper()
	addr := env.Addr.SMTPPlain
	if mode == smtpc.TLSImplicit {
		addr = env.Addr.SMTPTLS
	}
	s, err := smtpc.Open(context.Background(), smtpc.SessionOptions{
		Addr: addr, TLS: mode, InsecureSkipVerify: true,
		Username: user, Password: pass,
		ConnectTimeout: 5 * time.Second, CommandTimeout: 5 * time.Second, DataTimeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestSMTPSendAndDeliver(t *testing.T) {
	env := simtest.Start(t)
	ctx := context.Background()
	_ = env.Client.CreateUser(ctx, "smtp", "p")
	s := openSMTP(t, env, "smtp", "p", smtpc.TLSNone)
	body := "From: smtp@t\r\nTo: smtp@t\r\nSubject: s\r\n\r\nhello\r\n"
	if _, err := s.Send(ctx, smtpc.Envelope{From: "smtp@t", To: []string{"smtp@t"}}, strings.NewReader(body), smtpc.Hooks{}); err != nil {
		t.Fatal(err)
	}
	msgs, err := env.Client.ListSMTPMessages(ctx)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("list: %v %d", err, len(msgs))
	}
	if msgs[0].From != "smtp@t" || len(msgs[0].Stages) == 0 {
		t.Fatalf("msg: %+v", msgs[0])
	}
	cl := dialIMAP(t, env, "smtp", "p", imapc.TLSNone)
	sel, err := cl.Select(ctx, "INBOX", false)
	if err != nil || sel.Exists < 1 {
		t.Fatalf("delivered to INBOX: %+v %v", sel, err)
	}
}

func TestSMTPImplicitTLS(t *testing.T) {
	env := simtest.Start(t)
	ctx := context.Background()
	_ = env.Client.CreateUser(ctx, "smtls", "p")
	s := openSMTP(t, env, "smtls", "p", smtpc.TLSImplicit)
	if _, err := s.Send(ctx, smtpc.Envelope{From: "a@t", To: []string{"b@t"}}, strings.NewReader("x"), smtpc.Hooks{}); err != nil {
		t.Fatal(err)
	}
}

func TestSMTPScriptedFaults(t *testing.T) {
	env := simtest.Start(t)
	ctx := context.Background()
	_ = env.Client.CreateUser(ctx, "sfault", "p")

	t.Run("rcptReject", func(t *testing.T) {
		_ = env.Client.SetSMTPScript(ctx, "sfault", simulator.SMTPScript{
			"rcptTo": {Code: 550, Enhanced: "5.1.1", Text: "no such user"},
		})
		s := openSMTP(t, env, "sfault", "p", smtpc.TLSNone)
		_, err := s.Send(ctx, smtpc.Envelope{From: "a@t", To: []string{"b@t"}}, strings.NewReader("x"), smtpc.Hooks{})
		if errs.CodeOf(err) != errs.CodeSMTPPermanent {
			t.Fatalf("got %v (%s)", err, errs.CodeOf(err))
		}
		_ = env.Client.ClearSMTPScript(ctx, "sfault")
	})

	t.Run("dropAfterData", func(t *testing.T) {
		_ = env.Client.SetSMTPScript(ctx, "sfault", simulator.SMTPScript{
			"dataEnd": {DropAfter: true},
		})
		s := openSMTP(t, env, "sfault", "p", smtpc.TLSNone)
		_, err := s.Send(ctx, smtpc.Envelope{From: "a@t", To: []string{"b@t"}}, strings.NewReader("body"), smtpc.Hooks{})
		if errs.CodeOf(err) != errs.CodeSMTPOutcomeUnknown {
			t.Fatalf("drop after DATA: %v (%s)", err, errs.CodeOf(err))
		}
		_ = env.Client.ClearSMTPScript(ctx, "sfault")
	})

	t.Run("neverAnswerDataEnd", func(t *testing.T) {
		_ = env.Client.SetSMTPScript(ctx, "sfault", simulator.SMTPScript{
			"dataEnd": {NeverAnswer: true},
		})
		s := openSMTP(t, env, "sfault", "p", smtpc.TLSNone)
		_, err := s.Send(ctx, smtpc.Envelope{From: "a@t", To: []string{"b@t"}}, strings.NewReader("body"), smtpc.Hooks{})
		if errs.CodeOf(err) != errs.CodeSMTPOutcomeUnknown {
			t.Fatalf("hang after DATA: %v (%s)", err, errs.CodeOf(err))
		}
		_ = env.Client.ClearSMTPScript(ctx, "sfault")
	})

	t.Run("tempDataEnd", func(t *testing.T) {
		_ = env.Client.SetSMTPScript(ctx, "sfault", simulator.SMTPScript{
			"dataEnd": {Code: 452, Enhanced: "4.3.1", Text: "disk full"},
		})
		s := openSMTP(t, env, "sfault", "p", smtpc.TLSNone)
		_, err := s.Send(ctx, smtpc.Envelope{From: "a@t", To: []string{"b@t"}}, strings.NewReader("body"), smtpc.Hooks{})
		if errs.CodeOf(err) != errs.CodeSMTPTemporary {
			t.Fatalf("temp: %v (%s)", err, errs.CodeOf(err))
		}
		_ = env.Client.ClearSMTPScript(ctx, "sfault")
	})
}
