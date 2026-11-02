package sandbox

import (
	"strings"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
)

func TestListHelpers(t *testing.T) {
	if listLimit(0) != defaultListLimit || listLimit(1000) != maxListLimit || listLimit(7) != 7 {
		t.Fatal("listLimit")
	}
	when := time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)
	c := Captured{ID: "cap_1", CapturedAt: when}
	if listSortKey(c, "_id") != "cap_1" {
		t.Fatal("listSortKey id")
	}
	if listSortKey(c, "capturedAt") != when.Format(time.RFC3339Nano) {
		t.Fatal("listSortKey capturedAt")
	}
	for _, bad := range []string{"", "***", encodeListCursor(listCursor{Field: "capturedAt"})} {
		if _, err := decodeListCursor(bad); err == nil {
			t.Fatalf("cursor %q was accepted", bad)
		}
	}
	token := encodeListCursor(listCursor{ID: "cap_1", Field: "capturedAt", Desc: true})
	if _, _, err := listQuery(nil, "_id", -1, 10, token, 0); err == nil {
		t.Fatal("a cursor of another sort field was accepted")
	}
	if _, _, err := listQuery(nil, "capturedAt", -1, 10,
		encodeListCursor(listCursor{ID: "cap_1", Field: "capturedAt", Desc: true, Key: 42}), 0); err == nil {
		t.Fatal("non-string key accepted")
	}
	if _, _, err := listQuery(nil, "capturedAt", -1, 10,
		encodeListCursor(listCursor{ID: "cap_1", Field: "capturedAt", Desc: true, Key: "yesterday"}), 0); err == nil {
		t.Fatal("bad time accepted")
	}
	if _, _, err := listSort("password"); errs.CodeOf(err) != errs.CodeSortNotAllowed {
		t.Fatal("bad sort")
	}
	if field, dir, err := listSort("-capturedAt"); err != nil || field != "capturedAt" || dir != -1 {
		t.Fatalf("sort: %s %d %v", field, dir, err)
	}
	if field, dir, err := listSort("id"); err != nil || field != "_id" || dir != 1 {
		t.Fatalf("sort id: %s %d %v", field, dir, err)
	}
	if escapeRegex(`a.b+c`) != `a\.b\+c` {
		t.Fatalf("escapeRegex: %q", escapeRegex(`a.b+c`))
	}
	m := listMatch(ListFilter{
		AccountID: "acc_1",
		From:      when,
		To:        when.Add(time.Hour),
		Text:      "Hello",
	})
	if m["accountId"] != "acc_1" {
		t.Fatal("account filter")
	}
	if _, ok := m["capturedAt"]; !ok {
		t.Fatal("date filter")
	}
	if _, ok := m["subject"]; !ok {
		t.Fatal("text filter")
	}
}

func TestValidateRewriteTo(t *testing.T) {
	if _, err := validateRewriteTo(""); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("empty: %v", err)
	}
	if _, err := validateRewriteTo("not-an-address"); errs.CodeOf(err) != errs.CodeInvalidAddress {
		t.Fatalf("bad: %v", err)
	}
	got, err := validateRewriteTo(" Test@Example.COM ")
	if err != nil || got != "test@example.com" {
		t.Fatalf("got %q (%v)", got, err)
	}
}

func TestViewOfAndHasScope(t *testing.T) {
	c := &Captured{
		ID: "cap_1", OrganizationID: "org_1", AccountID: "acc_1", OutboundID: "out_1",
		Subject: "Hi", CapturedAt: time.Now().UTC(),
	}
	v := ViewOf(c)
	if v.ID != c.ID || v.Subject != "Hi" || v.OutboundID != "out_1" {
		t.Fatalf("%+v", v)
	}
	if !hasScope([]string{"sandbox:write"}, "sandbox:read") {
		t.Fatal("write implies read")
	}
	if !hasScope([]string{"admin"}, "sandbox:write") {
		t.Fatal("admin")
	}
	if hasScope([]string{"logs:read"}, "sandbox:write") {
		t.Fatal("unrelated")
	}
	if !strings.Contains(PolicyCapture+PolicyDrop+PolicyRewrite+PolicyAllow, "capture") {
		t.Fatal("policies")
	}
}
