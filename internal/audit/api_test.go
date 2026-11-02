package audit

import (
	"context"
	"testing"

	"github.com/radiobros/emailbridge/internal/errs"
)

// TestListBadTimeIs422: a malformed from/to is a 422 validation_failed problem, never a 500.
func TestListBadTimeIs422(t *testing.T) {
	m := &Module{}
	for _, in := range []*listInput{{From: "yesterday"}, {To: "2026-13-01T00:00:00Z"}, {From: "2026-10-01"}} {
		if _, err := m.list(context.Background(), in); errs.CodeOf(err) != errs.CodeValidationFailed {
			t.Fatalf("%+v: %v", in, err)
		}
	}
	for _, v := range []string{"2026-10-01T00:00:00Z", "2026-10-01T00:00:00.123456+02:00"} {
		if got, err := parseTimeParam("from", v); err != nil || got == nil {
			t.Fatalf("%s: %v %v", v, got, err)
		}
	}
	if got, err := parseTimeParam("to", ""); got != nil || err != nil {
		t.Fatal("empty = unset")
	}
}
