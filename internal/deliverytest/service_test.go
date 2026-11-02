package deliverytest

import (
	"context"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/msgauth"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/submit"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

func TestParseTimeoutAndRecipient(t *testing.T) {
	d, err := parseTimeout("")
	if err != nil || d != defaultTimeout {
		t.Fatalf("default: %v %v", d, err)
	}
	if _, err := parseTimeout("11m"); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("max: %v", err)
	}
	s := &Service{Env: "production", TestRecipient: "probe@example.com"}
	if err := s.checkRecipient("other@example.com"); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("prod block: %v", err)
	}
	if err := s.checkRecipient("probe@example.com"); err != nil {
		t.Fatal(err)
	}
	s.Env = "development"
	if err := s.checkRecipient("anyone@x.test"); err != nil {
		t.Fatal(err)
	}
}

func TestViewOfAndAuthFromRaw(t *testing.T) {
	raw := []byte("Authentication-Results: mx.example; spf=pass smtp.mailfrom=a@b.com\r\n" +
		"From: a@b.com\r\n\r\nbody\r\n")
	got := authFromRaw(raw, []string{"mx.example"})
	if got == nil || got.SPF.Result != "pass" {
		t.Fatalf("auth: %+v", got)
	}
	p := errs.ProblemFrom(errs.New(errs.CodeDeliveryTestTimeout, "late"), "", "")
	v := ViewOf(&Test{ID: "dtt_1", State: StateTimedOut, ErrorJSON: encodeProblem(p)})
	if v.Error == nil || v.Error.Code != errs.CodeDeliveryTestTimeout {
		t.Fatalf("view error: %+v", v.Error)
	}
	_ = msgauth.Results{}
}

type stubSubmit struct {
	res *submit.SubmitResult
	err error
}

func (s stubSubmit) Submit(context.Context, string, submit.SubmitRequest, string, bool) (*submit.SubmitResult, error) {
	return s.res, s.err
}

func TestCreateValidation(t *testing.T) {
	org := store.NewID(store.PrefixOrganization)
	ctx := tenancy.WithCaller(store.WithOrganization(context.Background(), org), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: "u", OrganizationID: org, Role: tenancy.RoleOwner,
	})
	s := &Service{Repo: &Repo{}, Submit: stubSubmit{}, Jobs: nil, Env: "development"}
	_, err := s.Create(ctx, CreateInput{AccountID: store.NewID(store.PrefixAccount), To: "a@b.com"})
	if errs.CodeOf(err) != errs.CodeInternalError {
		t.Fatalf("no jobs: %v", err)
	}
	_, err = s.Create(ctx, CreateInput{AccountID: "bad", To: "a@b.com"})
	if errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatalf("bad account: %v", err)
	}
	_, err = s.Create(ctx, CreateInput{AccountID: store.NewID(store.PrefixAccount), To: "not-an-email"})
	if errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("bad to: %v", err)
	}
	if s.pollEvery() != 500*time.Millisecond {
		t.Fatal("default poll")
	}
	s.PollEvery = time.Millisecond
	if s.pollEvery() != time.Millisecond {
		t.Fatal("poll override")
	}
	if s.now().IsZero() {
		t.Fatal("now")
	}
	s.Now = func() time.Time { return time.Unix(1, 0).UTC() }
	if !s.now().Equal(time.Unix(1, 0).UTC()) {
		t.Fatal("now inject")
	}
	_ = encodeProblem(nil)
}
