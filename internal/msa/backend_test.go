package msa

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-sasl"
	"github.com/emersion/go-smtp"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/submit"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

type memSubmit struct {
	lastAcc string
	lastReq submit.SubmitRequest
	err     error
	res     *submit.SubmitResult
}

func (m *memSubmit) Submit(_ context.Context, accountID string, req submit.SubmitRequest, _ string, _ bool) (*submit.SubmitResult, error) {
	m.lastAcc, m.lastReq = accountID, req
	if m.err != nil {
		return nil, m.err
	}
	if m.res != nil {
		return m.res, nil
	}
	return &submit.SubmitResult{Accepted: &submit.Accepted{ID: "out_1", State: "queued"}, Status: 202}, nil
}

type alwaysOn struct{}

func (alwaysOn) MSAEnabled(context.Context) bool { return true }

type alwaysOff struct{}

func (alwaysOff) MSAEnabled(context.Context) bool { return false }

func TestSessionSubmit(t *testing.T) {
	accID := store.NewID(store.PrefixAccount)
	org := store.NewID(store.PrefixOrganization)
	sub := &memSubmit{}
	s := &Session{
		b: &Backend{Submit: sub, MaxBytes: 1024, MaxRcpt: 10},
		authed: &Authed{
			Account: &AccountRef{ID: accID, OrganizationID: org, State: "connected"},
			Caller:  tenancy.Caller{Kind: tenancy.CallerSystem, OrganizationID: org},
		},
	}
	if err := s.Mail("me@ex.com", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Rcpt("you@ex.com", nil); err != nil {
		t.Fatal(err)
	}
	raw := "From: me@ex.com\r\nTo: you@ex.com\r\nSubject: hi\r\n\r\nbody\r\n"
	if err := s.Data(strings.NewReader(raw)); err != nil {
		t.Fatal(err)
	}
	if sub.lastAcc != accID {
		t.Fatalf("submit account %s", sub.lastAcc)
	}
	decoded, _ := base64.StdEncoding.DecodeString(sub.lastReq.Raw)
	if !strings.Contains(string(decoded), "Subject: hi") {
		t.Fatalf("raw = %q", decoded)
	}
	// The SMTP envelope reaches submit: RCPT TO decides delivery, blind copies included (P7.1 §2.3).
	if env := sub.lastReq.Envelope; env == nil || env.From != "me@ex.com" || len(env.To) != 1 || env.To[0] != "you@ex.com" {
		t.Fatalf("envelope = %+v", sub.lastReq.Envelope)
	}
	s.Reset()
	_ = s.Logout()
}

func TestSessionAuthRequired(t *testing.T) {
	s := &Session{b: &Backend{MaxRcpt: 1}}
	if err := s.Mail("a@b.c", nil); !errors.Is(err, smtp.ErrAuthRequired) {
		t.Fatalf("%v", err)
	}
	if err := s.Rcpt("a@b.c", nil); !errors.Is(err, smtp.ErrAuthRequired) {
		t.Fatalf("%v", err)
	}
	if err := s.Data(strings.NewReader("x")); !errors.Is(err, smtp.ErrAuthRequired) {
		t.Fatalf("%v", err)
	}
}

func TestSessionTooLargeAndRate(t *testing.T) {
	accID := store.NewID(store.PrefixAccount)
	org := store.NewID(store.PrefixOrganization)
	s := &Session{
		b: &Backend{MaxBytes: 10, MaxRcpt: 50, MsgRate: newLimiter(1), Submit: &memSubmit{}},
		authed: &Authed{
			Account: &AccountRef{ID: accID, OrganizationID: org},
			Caller:  tenancy.Caller{Kind: tenancy.CallerSystem, OrganizationID: org},
		},
		rcpt: []string{"a@b.c"},
	}
	err := s.Data(strings.NewReader("01234567890123456789"))
	var se *smtp.SMTPError
	if !errors.As(err, &se) || se.Code != 552 {
		t.Fatalf("%v", err)
	}
	s2 := &Session{
		b: &Backend{MaxBytes: 1000, MsgRate: newLimiter(1), Submit: &memSubmit{}},
		authed: &Authed{
			Account: &AccountRef{ID: accID, OrganizationID: org},
			Caller:  tenancy.Caller{Kind: tenancy.CallerSystem, OrganizationID: org},
		},
		rcpt: []string{"a@b.c"},
	}
	raw := "From: a@b.c\r\nTo: a@b.c\r\n\r\nx"
	if err := s2.Data(strings.NewReader(raw)); err != nil {
		t.Fatal(err)
	}
	if err := s2.Data(strings.NewReader(raw)); err == nil {
		t.Fatal("expected rate limit")
	}
}

func TestSessionMaxRcpt(t *testing.T) {
	s := &Session{
		b:      &Backend{MaxRcpt: 1},
		authed: &Authed{Account: &AccountRef{ID: "a"}},
		rcpt:   []string{"one@x"},
	}
	err := s.Rcpt("two@x", nil)
	var se *smtp.SMTPError
	if !errors.As(err, &se) || se.Code != 452 {
		t.Fatalf("%v", err)
	}
}

func TestMapSubmitErr(t *testing.T) {
	cases := []struct {
		err  error
		code int
	}{
		{errs.New(errs.CodeValidationFailed, "x"), 550},
		{errs.New(errs.CodeRateLimited, "x"), 451},
		{errs.New(errs.CodeForbidden, "x"), 550},
		{errs.New(errs.CodeNotFound, "x"), 550},
		{context.DeadlineExceeded, 451},
		{errors.New("other"), 451},
	}
	for _, c := range cases {
		err := mapSubmitErr(c.err)
		var se *smtp.SMTPError
		if !errors.As(err, &se) || se.Code != c.code {
			t.Fatalf("%v → %v want %d", c.err, err, c.code)
		}
	}
}

func TestBackendGateOff(t *testing.T) {
	be := &Backend{Gate: alwaysOff{}}
	if _, err := be.NewSession(nil); err == nil {
		t.Fatal("expected unavailable")
	}
}

func TestSMTPServerRoundTrip(t *testing.T) {
	accID := store.NewID(store.PrefixAccount)
	org := store.NewID(store.PrefixOrganization)
	hash, _ := hashPassword("submission-pass-ok")
	sub := &memSubmit{}
	be := &Backend{
		Auth: &Authenticator{
			Accounts: &memAccounts{byID: map[string]*AccountRef{
				accID: {ID: accID, OrganizationID: org, State: "connected"},
			}},
			Creds: memCredStore{accID: {Hash: hash}},
		},
		Submit: sub, MaxBytes: 1 << 20, MaxRcpt: 10, Gate: alwaysOn{},
	}
	s := smtp.NewServer(be)
	s.Domain = "test"
	s.AllowInsecureAuth = true
	s.MaxMessageBytes = 1 << 20
	s.ReadTimeout = 5 * time.Second
	s.WriteTimeout = 5 * time.Second
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	go func() { _ = s.Serve(ln) }()

	c, err := smtp.Dial(ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if err := c.Hello("localhost"); err != nil {
		t.Fatal(err)
	}
	auth := sasl.NewPlainClient("", accID, "submission-pass-ok")
	if err := c.Auth(auth); err != nil {
		t.Fatal(err)
	}
	if err := c.Mail("me@ex.com", nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Rcpt("you@ex.com", nil); err != nil {
		t.Fatal(err)
	}
	w, err := c.Data()
	if err != nil {
		t.Fatal(err)
	}
	msg := "From: me@ex.com\r\nTo: you@ex.com\r\nSubject: t\r\n\r\nbody"
	if _, err := io.WriteString(w, msg); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if sub.lastAcc != accID {
		t.Fatalf("submit not called: %q", sub.lastAcc)
	}
}

func TestSessionLoginAuth(t *testing.T) {
	accID := store.NewID(store.PrefixAccount)
	org := store.NewID(store.PrefixOrganization)
	hash, _ := hashPassword("submission-pass-ok")
	s := &Session{
		b: &Backend{
			Auth: &Authenticator{
				Accounts: &memAccounts{byID: map[string]*AccountRef{
					accID: {ID: accID, OrganizationID: org, State: "connected"},
				}},
				Creds: memCredStore{accID: {Hash: hash}},
			},
		},
		remote: "127.0.0.1:9",
	}
	if err := s.login(accID, "submission-pass-ok"); err != nil {
		t.Fatal(err)
	}
	if s.authed == nil {
		t.Fatal("not authed")
	}
	mechs := s.AuthMechanisms()
	if len(mechs) != 2 {
		t.Fatalf("%v", mechs)
	}
	if _, err := s.Auth("UNKNOWN"); err == nil {
		t.Fatal("expected unknown mech")
	}
	plain, err := s.Auth(sasl.Plain)
	if err != nil {
		t.Fatal(err)
	}
	_ = plain
}
