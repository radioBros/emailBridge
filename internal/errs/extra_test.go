package errs

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/danielgtaylor/huma/v2"
)

func TestEErrorBranches(t *testing.T) {
	t.Parallel()
	var nilE *E
	if nilE.Error() != "" || nilE.Unwrap() != nil {
		t.Fatal("nil E")
	}
	cases := []struct {
		e    *E
		want string
	}{
		{&E{Op: "op", Msg: "msg", Err: errors.New("root")}, "op: msg: root"},
		{&E{Op: "op", Msg: "msg"}, "op: msg"},
		{&E{Op: "op", Err: errors.New("root")}, "op: root"},
		{&E{Msg: "msg", Err: errors.New("root")}, "msg: root"},
		{&E{Err: errors.New("root")}, "root"},
		{&E{Code: CodeNotFound}, "not_found"},
		{&E{}, "error"},
	}
	for _, tc := range cases {
		if got := tc.e.Error(); got != tc.want {
			t.Fatalf("%q want %q", got, tc.want)
		}
	}
}

func TestLookupAndKindDefaults(t *testing.T) {
	t.Parallel()
	kind, status, title, ok := Lookup(CodeNotFound)
	if !ok || kind != KindNotFound || status != 404 || title == "" {
		t.Fatal(kind, status, title, ok)
	}
	if _, _, _, ok := Lookup(Code("nope")); ok {
		t.Fatal("want miss")
	}
	for _, k := range []Kind{
		KindTemporary, KindPermanent, KindOutcomeUnknown, KindAuth, KindForbidden,
		KindValidation, KindConfig, KindRateLimited, KindNotFound, KindConflict, KindInternal,
		Kind("other"),
	} {
		if kindDefaultStatus(k) == 0 {
			t.Fatalf("%s", k)
		}
	}
}

func TestMapHTTPStatus(t *testing.T) {
	t.Parallel()
	cases := []struct {
		status int
		code   Code
	}{
		{422, CodeValidationFailed},
		{404, CodeNotFound},
		{405, CodeMalformedRequest},
		{413, CodePayloadTooLarge},
		{415, CodeUnsupportedMediaType},
		{429, CodeRateLimited},
		{400, CodeMalformedRequest},
		{401, CodeMalformedRequest},
		{500, CodeInternalError},
		{502, CodeInternalError},
		{200, CodeInternalError},
	}
	for _, tc := range cases {
		code, _ := mapHTTPStatus(tc.status)
		if code != tc.code {
			t.Fatalf("%d -> %s want %s", tc.status, code, tc.code)
		}
	}
}

func TestProblemNilAndInternal(t *testing.T) {
	t.Parallel()
	var p *Problem
	if p.Error() != "" || p.GetStatus() != http.StatusInternalServerError {
		t.Fatal("nil problem")
	}
	p = &Problem{Title: "T"}
	if p.Error() != "T" {
		t.Fatal(p.Error())
	}
	p = &Problem{Code: CodeNotFound}
	if p.Error() != "not_found" {
		t.Fatal(p.Error())
	}

	p = ProblemFrom(nil, "r", "t")
	if p.Detail != "An internal error occurred." || p.Status != 500 {
		t.Fatalf("%+v", p)
	}

	e := &E{Kind: KindInternal, Code: CodeInternalError, Err: errors.New("secret")}
	p = ProblemFrom(e, "", "")
	if p.Detail != "An internal error occurred." {
		t.Fatal(p.Detail)
	}

	e = &E{Kind: KindValidation, Code: Code("custom_x"), Msg: "m"}
	p = ProblemFrom(e, "", "")
	if p.Status != 400 || p.Detail != "m" {
		t.Fatalf("%+v", p)
	}

	e2 := New(Code("totally_unknown"), "x")
	if e2.Kind != KindInternal {
		t.Fatal(e2.Kind)
	}
	_ = Wrap(fmt.Errorf("x"), Code("totally_unknown"), "op")
}

func TestHumaStatusMappingAndDetails(t *testing.T) {
	InstallHuma()
	for _, status := range []int{405, 413, 415, 429, 500, 400} {
		p := newHumaError(status, "m").(*Problem)
		if p.Status != status {
			t.Fatalf("%d -> %d", status, p.Status)
		}
	}
	detail := &huma.ErrorDetail{Location: "body.email", Message: "bad"}
	p := newHumaError(422, "validation failed", detail).(*Problem)
	if len(p.Errors) != 1 || p.Errors[0].Path != "body.email" || p.Errors[0].Code != "invalid" {
		t.Fatalf("%+v", p.Errors)
	}
	// Exercise NewErrorWithContext assignment.
	se := huma.NewErrorWithContext(nil, 404, "gone")
	if se.GetStatus() != 404 {
		t.Fatal(se.GetStatus())
	}
}
