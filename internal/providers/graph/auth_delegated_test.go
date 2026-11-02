package graph

import (
	"context"
	"testing"

	"github.com/radiobros/emailbridge/internal/errs"
)

func TestDelegatedSource(t *testing.T) {
	t.Parallel()
	d := DelegatedSource{Parent: StaticToken{Token: "tok", Username: "parent@ex.com"}, UPN: "shared@ex.com"}
	tok, user, err := d.AccessToken(context.Background(), "acc_x")
	if err != nil || tok != "tok" || user != "shared@ex.com" {
		t.Fatalf("%s %s %v", tok, user, err)
	}
	if _, _, err := (DelegatedSource{}).AccessToken(context.Background(), "x"); errs.CodeOf(err) != errs.CodeOauthNotConnected {
		t.Fatalf("%v", err)
	}
}
