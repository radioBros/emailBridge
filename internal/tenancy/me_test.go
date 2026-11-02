package tenancy

import (
	"context"
	"strings"
	"testing"

	"github.com/radiobros/emailbridge/internal/errs"
)

func TestPatchMeAuthAndValidation(t *testing.T) {
	s := &Service{Repo: &Repo{}}
	ctx := context.Background()
	if _, err := s.PatchMe(ctx, MePatch{}); errs.CodeOf(err) != errs.CodeUnauthenticated {
		t.Fatalf("anon: %v", err)
	}
	keyCtx := WithCaller(ctx, Caller{Kind: CallerAPIKey, APIKeyID: "key_1"})
	if _, err := s.PatchMe(keyCtx, MePatch{}); errs.CodeOf(err) != errs.CodeForbidden {
		t.Fatalf("api key: %v", err)
	}
	userCtx := WithCaller(ctx, Caller{Kind: CallerUser, UserID: "usr_x"})
	empty := "   "
	if _, err := s.PatchMe(userCtx, MePatch{Name: &empty}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("empty name: %v", err)
	}
	long := strings.Repeat("x", 101)
	if _, err := s.PatchMe(userCtx, MePatch{Name: &long}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("long name: %v", err)
	}
	bad := "xx"
	if _, err := s.PatchMe(userCtx, MePatch{Locale: &bad}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("bad locale: %v", err)
	}
}
