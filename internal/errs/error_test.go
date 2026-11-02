package errs

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestNewWrapKindOf(t *testing.T) {
	t.Parallel()
	e := New(CodeNotFound, "missing")
	if e.Kind != KindNotFound || e.Code != CodeNotFound {
		t.Fatalf("%+v", e)
	}
	if e.Error() != "missing" {
		t.Fatal(e.Error())
	}
	wrapped := Wrap(fmt.Errorf("root"), CodeTimeout, "fetch")
	if !errors.Is(wrapped, wrapped.Err) {
		t.Fatal("unwrap")
	}
	if KindOf(wrapped) != KindTemporary {
		t.Fatal(KindOf(wrapped))
	}
	if CodeOf(wrapped) != CodeTimeout {
		t.Fatal(CodeOf(wrapped))
	}
	if !IsKind(e, KindNotFound) {
		t.Fatal("IsKind")
	}
	if !Retryable(wrapped) || Retryable(e) {
		t.Fatal("Retryable")
	}
	if Reportable(New(CodeInternalError, "x")) != true {
		t.Fatal("internal reportable")
	}
	if Reportable(New(CodeConfigDrift, "x")) != false {
		// config_drift is conflict kind
		if KindOf(New(CodeConfigDrift, "x")) != KindConflict {
			t.Fatal(KindOf(New(CodeConfigDrift, "x")))
		}
	}
	if Reportable(New(CodeMailTLSFailed, "x")) != true {
		t.Fatal("config kind should be reportable")
	}
	for _, code := range []Code{CodeNotFound, CodeConflict, CodeRateLimited, CodeValidationFailed, CodeUnauthenticated, CodeForbidden} {
		if Reportable(New(code, "x")) {
			t.Fatalf("%s should not be reportable", code)
		}
	}
}

func TestContextErrors(t *testing.T) {
	t.Parallel()
	if KindOf(context.DeadlineExceeded) != KindTemporary || CodeOf(context.DeadlineExceeded) != CodeTimeout {
		t.Fatal("deadline")
	}
	if KindOf(context.Canceled) != KindTemporary {
		t.Fatal("canceled")
	}
	if KindOf(errors.New("plain")) != KindInternal {
		t.Fatal("plain")
	}
}

func TestWithSafe(t *testing.T) {
	t.Parallel()
	e := New(CodeNotFound, "x").WithSafe("id", "abc")
	if e.Safe["id"] != "abc" {
		t.Fatal(e.Safe)
	}
}

func TestProblemFrom(t *testing.T) {
	t.Parallel()
	p := ProblemFrom(New(CodeNotFound, "gone"), "req", "tr")
	if p.Status != 404 || p.Code != CodeNotFound || p.Detail != "gone" {
		t.Fatalf("%+v", p)
	}
	if p.RequestID != "req" || p.TraceID != "tr" {
		t.Fatal(p)
	}
	if p.Type != problemTypeBase+"not_found" {
		t.Fatal(p.Type)
	}
	if p.ContentType("anything") != "application/problem+json" {
		t.Fatal(p.ContentType(""))
	}
	if p.GetStatus() != 404 || p.Error() != "gone" {
		t.Fatal(p.Error())
	}

	// wrapped E
	p = ProblemFrom(fmt.Errorf("outer: %w", New(CodeValidationFailed, "bad")), "", "")
	if p.Code != CodeValidationFailed || p.Status != 422 {
		t.Fatalf("%+v", p)
	}

	// plain error — no leak
	sentinel := "TOP_SECRET_INTERNAL_DETAIL"
	p = ProblemFrom(errors.New(sentinel), "", "")
	if p.Code != CodeInternalError || p.Status != 500 {
		t.Fatalf("%+v", p)
	}
	if strings.Contains(p.Detail, sentinel) || strings.Contains(p.Error(), sentinel) {
		t.Fatalf("leaked: %s", p.Detail)
	}

	// attempt-only → kind default status
	p = ProblemFrom(New(CodeSMTPTemporary, "tmp"), "", "")
	if p.Status != 503 {
		t.Fatalf("attempt-only status %d", p.Status)
	}
}
