package webhook

import (
	"errors"
	"strings"
	"testing"

	"github.com/radiobros/emailbridge/internal/errs"
)

func TestClassifySinkErrorTransient(t *testing.T) {
	out := classifySinkError(errors.New("sinks.nats: dial tcp 127.0.0.1:1: connectex: No connection could be made because the target machine actively refused it."))
	if !out.Retry || out.Attempt.Kind != string(errs.KindTemporary) {
		t.Fatalf("%+v", out)
	}
	perm := classifySinkError(errs.New(errs.CodeValidationFailed, "bad"))
	if perm.Retry {
		t.Fatalf("validation should not retry: %+v", perm)
	}
}

func TestIsTransientKind(t *testing.T) {
	if !isTransientKind(errs.KindTemporary) || !isTransientKind(errs.KindRateLimited) ||
		!isTransientKind(errs.KindOutcomeUnknown) || isTransientKind(errs.KindPermanent) {
		t.Fatal("kind map")
	}
	out := classifySinkError(errors.New("throttling: slow down please"))
	if out.Attempt.Kind != string(errs.KindTemporary) && !strings.Contains(out.Attempt.Error, "throttling") {
		t.Fatalf("%+v", out)
	}
	_ = classifySinkError(errors.New("service unavailable"))
	_ = classifySinkError(errors.New("i/o timeout on write"))
	_ = classifySinkError(errors.New("connection reset by peer"))
}
