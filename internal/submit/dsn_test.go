package submit

import (
	"strings"
	"testing"

	"github.com/radiobros/emailbridge/internal/errs"
)

func TestValidateDSN(t *testing.T) {
	if err := validateDSN(nil); err != nil {
		t.Fatal(err)
	}
	if err := validateDSN(&DSNRequest{Notify: []string{"success", "failure"}, Ret: "hdrs", EnvID: "e1"}); err != nil {
		t.Fatal(err)
	}
	if err := validateDSN(&DSNRequest{Notify: []string{"never"}}); err != nil {
		t.Fatal(err)
	}
	if err := validateDSN(&DSNRequest{Notify: []string{"never", "failure"}}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("never+other: %v", err)
	}
	if err := validateDSN(&DSNRequest{Ret: "body"}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("bad ret: %v", err)
	}
	if err := validateDSN(&DSNRequest{Notify: []string{"boom"}}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("bad notify: %v", err)
	}
	if err := validateDSN(&DSNRequest{EnvID: strings.Repeat("x", 101)}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("long envid: %v", err)
	}
}
