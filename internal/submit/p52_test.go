package submit

import (
	"context"
	"strings"
	"testing"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/tracking"
)

type stubSuppression struct {
	err    error
	listID string
}

func (s *stubSuppression) CheckAddresses(_ context.Context, _ string, _ []string, mailingListID string) error {
	s.listID = mailingListID
	return s.err
}

type stubTracking struct {
	opens, clicks, unsub bool
	opensLocked          bool
	applied              bool
	last                 tracking.PrepareInput
}

func (s *stubTracking) SwitchState(context.Context, string, string) (bool, bool, bool) {
	return s.opens, s.clicks, s.unsub
}

func (s *stubTracking) TrackState(context.Context, string, string) tracking.TrackState {
	return tracking.TrackState{
		Opens: s.opens, Clicks: s.clicks, Unsub: s.unsub, OpensLocked: s.opensLocked,
	}
}

func (s *stubTracking) Apply(_ context.Context, in tracking.PrepareInput) (*tracking.PrepareResult, error) {
	s.applied = true
	s.last = in
	h := in.Headers
	if h == nil {
		h = map[string]string{}
	}
	if in.ListUnsub {
		h["List-Unsubscribe"] = "<https://eb.example/u/tok>"
	}
	if in.ListID != "" {
		h["List-Id"] = "<" + in.ListID + ">"
	}
	html := in.HTML
	if in.TrackOpens {
		html += "<!--t-->"
	}
	return &tracking.PrepareResult{HTML: html, Headers: h}, nil
}

func TestRecipientAddressesAndSuppression(t *testing.T) {
	req := SubmitRequest{
		To: []AddressInput{{Address: "a@x.test"}}, Cc: []AddressInput{{Address: "b@x.test"}},
	}
	got := recipientAddresses(req)
	if len(got) != 2 {
		t.Fatalf("%v", got)
	}
	sup := &stubSuppression{err: errs.New(errs.CodeRecipientSuppressed, "nope")}
	s := &Service{Suppression: sup}
	if err := s.checkSuppression(context.Background(), "org_x", req); errs.CodeOf(err) != errs.CodeRecipientSuppressed {
		t.Fatal(err)
	}
	if err := (&Service{}).checkSuppression(context.Background(), "org_x", req); err != nil {
		t.Fatal(err)
	}
	req.ListID = "news.example.com"
	sup.err = nil
	if err := s.checkSuppression(context.Background(), "org_x", req); err != nil || sup.listID != "news.example.com" {
		t.Fatalf("%v %q", err, sup.listID)
	}
}

func TestApplyTrackingStub(t *testing.T) {
	tr := &stubTracking{opens: true, clicks: true, unsub: true}
	s := &Service{Tracking: tr}
	req := SubmitRequest{HTML: "<p>hi</p>", To: []AddressInput{{Address: "a@x.test"}}}
	if !needsOutboundIDEarly(context.Background(), s, "org", "acc", req) {
		t.Fatal("early id")
	}
	if err := s.applyTracking(context.Background(), "org", "acc", "out_1", &req); err != nil {
		t.Fatal(err)
	}
	if !tr.applied || req.HTML == "<p>hi</p>" || req.Headers["List-Unsubscribe"] == "" {
		t.Fatalf("%+v", req)
	}
	if err := (&Service{}).applyTracking(context.Background(), "org", "acc", "out", &req); err != nil {
		t.Fatal(err)
	}
}

func TestApplyTrackingListIDWithoutInjector(t *testing.T) {
	s := &Service{}
	req := SubmitRequest{ListID: "news.example.com", HTML: "<p>x</p>"}
	if !needsOutboundIDEarly(context.Background(), s, "org", "acc", req) {
		t.Fatal("listId needs early id")
	}
	if err := s.applyTracking(context.Background(), "org", "acc", "out_1", &req); err != nil {
		t.Fatal(err)
	}
	if req.Headers["List-Id"] != "<news.example.com>" || req.ListID != "news.example.com" {
		t.Fatalf("%+v", req)
	}
}

func TestNormalizeListID(t *testing.T) {
	if got, err := normalizeListID("  <news.example.com>  "); err != nil || got != "news.example.com" {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := normalizeListID("bad\nid"); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	if _, err := normalizeListID(strings.Repeat("a", 201)); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
}

func TestApplyTrackingOverridesAndListID(t *testing.T) {
	tr := &stubTracking{opens: true, clicks: true, unsub: true}
	s := &Service{Tracking: tr}
	off := false
	req := SubmitRequest{
		HTML: "<p>hi</p>", To: []AddressInput{{Address: "a@x.test"}},
		TrackOpens: &off, TrackClicks: &off, ListID: "news.example.com",
	}
	if err := s.applyTracking(context.Background(), "org", "acc", "out_1", &req); err != nil {
		t.Fatal(err)
	}
	if tr.last.TrackOpens || tr.last.TrackClicks || tr.last.ListID != "news.example.com" {
		t.Fatalf("%+v", tr.last)
	}
	if req.Headers["List-Id"] != "<news.example.com>" {
		t.Fatalf("headers %+v", req.Headers)
	}
	tr.opensLocked = true
	tr.opens = true
	req2 := SubmitRequest{HTML: "<p>x</p>", To: []AddressInput{{Address: "a@x.test"}}, TrackOpens: &off}
	if err := s.applyTracking(context.Background(), "org", "acc", "out_2", &req2); err != nil {
		t.Fatal(err)
	}
	if !tr.last.TrackOpens {
		t.Fatal("locked opens should ignore off override")
	}
}
