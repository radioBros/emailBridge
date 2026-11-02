package connect

import (
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
)

func TestIdentityMatchAndCheck(t *testing.T) {
	pin := Pin(Observed{Email: "A@B.Test", IMAPUser: "A@B.Test"}, time.Unix(1, 0))
	if pin.Email != "a@b.test" || pin.IMAPUser != "a@b.test" {
		t.Fatalf("%+v", pin)
	}
	if !Match(&pin, Observed{Email: "a@b.test", IMAPUser: "a@b.test"}) {
		t.Fatal("match")
	}
	if Match(&pin, Observed{Email: "other@b.test", IMAPUser: "a@b.test"}) {
		t.Fatal("email mismatch")
	}
	if err := CheckIdentity(&pin, Observed{Email: "a@b.test", IMAPUser: "other"}); errs.CodeOf(err) != errs.CodeIdentityMismatch {
		t.Fatalf("%v", err)
	}
	if err := CheckIdentity(nil, Observed{Email: "x"}); err != nil {
		t.Fatal(err)
	}
	oauthPin := Pin(Observed{Email: "a@b.test", OAuthSubject: "sub1"}, time.Now())
	if !Match(&oauthPin, Observed{Email: "a@b.test", OAuthSubject: "sub1"}) {
		t.Fatal("oauth match")
	}
	if Match(&oauthPin, Observed{Email: "a@b.test", OAuthSubject: "other"}) {
		t.Fatal("oauth mismatch")
	}
}

func TestGuardCredentialChange(t *testing.T) {
	pin := Pin(Observed{Email: "a@b.test", IMAPUser: "a@b.test"}, time.Now())
	if _, err := GuardCredentialChange(&pin, Observed{Email: "a@b.test", IMAPUser: "evil"}, false); errs.CodeOf(err) != errs.CodeIdentityMismatch {
		t.Fatalf("%v", err)
	}
	got, err := GuardCredentialChange(&pin, Observed{Email: "a@b.test", IMAPUser: "evil"}, true)
	if err != nil || got.IMAPUser != "evil" {
		t.Fatalf("%+v %v", got, err)
	}
	got, err = GuardCredentialChange(nil, Observed{Email: "n@e.test", IMAPUser: "n"}, false)
	if err != nil || got.Email != "n@e.test" {
		t.Fatalf("%+v %v", got, err)
	}
	got, err = GuardCredentialChange(&pin, Observed{Email: "a@b.test", IMAPUser: "a@b.test"}, false)
	if err != nil || got == nil || got.Email != pin.Email {
		t.Fatalf("%+v %v", got, err)
	}
}
