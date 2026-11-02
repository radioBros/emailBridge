package msa

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/emersion/go-smtp"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/submit"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

func TestDataSubmitErrors(t *testing.T) {
	accID := store.NewID(store.PrefixAccount)
	org := store.NewID(store.PrefixOrganization)
	cases := []error{
		errs.New(errs.CodeValidationFailed, "x"),
		errs.New(errs.CodeRawAndFields, "x"),
		errs.New(errs.CodeFeatureDisabled, "x"),
		errs.New(errs.CodeUnauthenticated, "x"),
		errs.New(errs.CodeScopeMissing, "x"),
	}
	for _, want := range cases {
		sub := &memSubmit{err: want}
		s := &Session{
			b: &Backend{Submit: sub, MaxBytes: 1000},
			authed: &Authed{
				Account: &AccountRef{ID: accID, OrganizationID: org},
				Caller:  tenancy.Caller{Kind: tenancy.CallerSystem, OrganizationID: org},
			},
			rcpt: []string{"a@b.c"},
		}
		err := s.Data(strings.NewReader("From: a@b.c\r\nTo: a@b.c\r\n\r\nx"))
		var se *smtp.SMTPError
		if !errors.As(err, &se) {
			t.Fatalf("%v", err)
		}
	}
}

func TestDataNoRecipients(t *testing.T) {
	s := &Session{
		b:      &Backend{MaxBytes: 100},
		authed: &Authed{Account: &AccountRef{ID: "a"}},
	}
	err := s.Data(strings.NewReader("x"))
	var se *smtp.SMTPError
	if !errors.As(err, &se) || se.Code != 554 {
		t.Fatalf("%v", err)
	}
}

func TestDataNilResult(t *testing.T) {
	accID := store.NewID(store.PrefixAccount)
	org := store.NewID(store.PrefixOrganization)
	sub := &memSubmit{res: nil, err: nil}
	// force nil result by custom submitter
	s := &Session{
		b: &Backend{Submit: submitNil{}, MaxBytes: 1000},
		authed: &Authed{
			Account: &AccountRef{ID: accID, OrganizationID: org},
			Caller:  tenancy.Caller{Kind: tenancy.CallerSystem, OrganizationID: org},
		},
		rcpt: []string{"a@b.c"},
	}
	_ = sub
	err := s.Data(strings.NewReader("From: a@b.c\r\nTo: a@b.c\r\n\r\nx"))
	var se *smtp.SMTPError
	if !errors.As(err, &se) || se.Code != 451 {
		t.Fatalf("%v", err)
	}
}

type submitNil struct{}

func (submitNil) Submit(context.Context, string, submit.SubmitRequest, string, bool) (*submit.SubmitResult, error) {
	return nil, nil
}

func TestAuthLoginFail(t *testing.T) {
	s := &Session{
		b: &Backend{Auth: &Authenticator{
			Accounts: &memAccounts{byID: map[string]*AccountRef{}},
			Creds:    memCredStore{},
		}},
		remote: "127.0.0.1:1",
	}
	if err := s.login("nobody", "x"); err == nil {
		t.Fatal("expected fail")
	}
}

func TestServerTLSAddrRequiresCert(t *testing.T) {
	s := NewServer(Options{Enabled: true, AddrTLS: "127.0.0.1:0"}, &Backend{}, nil)
	if err := s.Start(); err == nil {
		t.Fatal("expected tls error")
	}
}

func TestVerifyPasswordEdge(t *testing.T) {
	if verifyPassword("v1$$", "x") || verifyPassword("v2$aa$bb", "x") {
		t.Fatal()
	}
	h, _ := hashPassword("abcdefghijkl")
	parts := strings.Split(h, "$")
	bad := parts[0] + "$" + parts[1] + "$zzzz"
	if verifyPassword(bad, "abcdefghijkl") {
		t.Fatal()
	}
}

func TestServiceBadAccountID(t *testing.T) {
	svc := &Service{Repo: &memRepo{}, OwnsAccount: ownsAll}
	org := store.NewID(store.PrefixOrganization)
	ctx := tenancy.WithCaller(store.WithOrganization(context.Background(), org), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: "u", OrganizationID: org, Role: tenancy.RoleAdmin,
	})
	if _, err := svc.Status(ctx, "not-an-id"); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatalf("%v", err)
	}
}

func TestLimiterNil(t *testing.T) {
	var l *limiter
	if !l.allow("x") {
		t.Fatal()
	}
}
