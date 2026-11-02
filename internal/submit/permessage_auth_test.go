package submit

import (
	"context"
	"testing"

	"github.com/radiobros/emailbridge/internal/errs"
)

func TestPerMessageOpsNeedACaller(t *testing.T) {
	s := &Service{}
	ctx := context.Background()
	if _, err := s.Get(ctx, "out_x"); errs.CodeOf(err) != errs.CodeUnauthenticated {
		t.Fatalf("get: %v", err)
	}
	if _, err := s.Retry(ctx, "out_x", ""); errs.CodeOf(err) != errs.CodeUnauthenticated {
		t.Fatalf("retry: %v", err)
	}
	if _, err := s.SendNow(ctx, "out_x", ""); errs.CodeOf(err) != errs.CodeUnauthenticated {
		t.Fatalf("send-now: %v", err)
	}
}
