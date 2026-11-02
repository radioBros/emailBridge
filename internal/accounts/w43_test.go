package accounts

import (
	"context"
	"testing"

	"github.com/radiobros/emailbridge/internal/errs"
)

func TestBindOAuth_nilHook(t *testing.T) {
	s := &Service{}
	if err := s.bindOAuth(context.Background(), "acc_x", "oap_y", true); errs.CodeOf(err) != errs.CodeInternalError {
		t.Fatalf("%v", err)
	}
}

func TestBindOAuth_callsHook(t *testing.T) {
	var gotAcc, gotApp string
	var gotSkip bool
	s := &Service{
		BindOAuthApp: func(_ context.Context, accountID, appID string, skipVerify bool) error {
			gotAcc, gotApp, gotSkip = accountID, appID, skipVerify
			return nil
		},
	}
	if err := s.bindOAuth(context.Background(), "acc_1", "  oap_1  ", true); err != nil {
		t.Fatal(err)
	}
	if gotAcc != "acc_1" || gotApp != "oap_1" || !gotSkip {
		t.Fatalf("%q %q %v", gotAcc, gotApp, gotSkip)
	}
}
