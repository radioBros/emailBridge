package accounts

import (
	"testing"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
)

func TestValidateDelegatedShape(t *testing.T) {
	t.Parallel()
	if err := validateDelegatedShape(nil); err != nil {
		t.Fatal(err)
	}
	if errs.CodeOf(validateDelegatedShape(&DelegatedRef{})) != errs.CodeDelegatedInvalid {
		t.Fatal("empty account")
	}
	id := store.NewID(store.PrefixAccount)
	if errs.CodeOf(validateDelegatedShape(&DelegatedRef{Account: id})) != errs.CodeDelegatedInvalid {
		t.Fatal("empty user")
	}
	if err := validateDelegatedShape(&DelegatedRef{Account: id, User: "shared@ex.com"}); err != nil {
		t.Fatal(err)
	}
}

func TestDelegatedHelpers(t *testing.T) {
	t.Parallel()
	parent := store.NewID(store.PrefixAccount)
	a := &Account{ID: store.NewID(store.PrefixAccount), Delegated: &DelegatedRef{Account: parent, User: " shared@ex.com "}}
	if a.DelegatedTokenSource() != parent {
		t.Fatal(a.DelegatedTokenSource())
	}
	if a.DelegatedUPN() != "shared@ex.com" {
		t.Fatal(a.DelegatedUPN())
	}
	if !a.UsesOAuth() {
		t.Fatal("delegated implies oauth")
	}
	plain := &Account{ID: "x"}
	if plain.DelegatedTokenSource() != "x" {
		t.Fatal("self")
	}
}

func TestValidateCreateAuthServerNoPass(t *testing.T) {
	t.Parallel()
	in := CreateInput{
		Name: "A", Email: "a@ex.com",
		Auth: &AuthInput{UseAuthServer: true},
		IMAP: IMAPConfigInput{Host: "imap.ex", Port: 993, TLS: TLSImplicit, Auth: MailAuthInput{User: "u"}},
		SMTP: SMTPConfigInput{Host: "smtp.ex", Port: 465, TLS: TLSImplicit, Auth: MailAuthInput{User: "u"}},
	}
	if err := validateCreate(in, nil); err != nil {
		t.Fatal(err)
	}
}
