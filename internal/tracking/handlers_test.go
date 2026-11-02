package tracking

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
)

func TestHandlersOpenClickUnsub(t *testing.T) {
	key := []byte("test-tracking-sign-key-32-bytes!!")
	org := store.NewID(store.PrefixOrganization)
	out := store.NewID(store.PrefixOutbound)
	exp := time.Now().UTC().Add(time.Hour)
	svc := &Service{
		SignKey: key, PublicURL: "https://eb.example", TTL: time.Hour,
		Repo: &Repo{}, // nil DB — insertEvent skipped when Repo.DB nil? insert will panic
		Now:  func() time.Time { return time.Now().UTC() },
	}
	// Repo with nil DB: avoid calling record's insert — use nil Repo
	svc.Repo = nil
	unsubCalled := false
	svc.Unsub = func(_ context.Context, o, addr, _, _ string) error {
		unsubCalled = true
		if o != org || addr != "user@example.test" {
			t.Fatalf("%s %s", o, addr)
		}
		return nil
	}

	openTok, err := Sign(key, Claims{Kind: KindOpen, OrgID: org, OutboundID: out, Recipient: "user@example.test", Exp: exp})
	if err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/t/o/"+openTok, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	svc.HandleOpen(rr, req, openTok)
	if rr.Code != 200 || rr.Header().Get("Content-Type") != "image/gif" || len(rr.Body.Bytes()) < 10 {
		t.Fatalf("open: %d %s", rr.Code, rr.Body.String())
	}

	// click without repo → getLink fails
	clickTok, _ := Sign(key, Claims{Kind: KindClick, OrgID: org, OutboundID: out, Recipient: "user@example.test", Index: 0, Exp: exp})
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/t/c/"+clickTok, nil)
	svc.HandleClick(rr, req, clickTok)
	if rr.Code == 302 {
		t.Fatal("expected missing link error")
	}

	unsubTok, _ := Sign(key, Claims{Kind: KindUnsub, OrgID: org, OutboundID: out, Recipient: "user@example.test", Exp: exp})
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/u/"+unsubTok, nil)
	svc.HandleUnsubGET(rr, req, unsubTok)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "Unsubscribe") {
		t.Fatalf("get: %s", rr.Body.String())
	}
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/u/"+unsubTok, strings.NewReader("List-Unsubscribe=One-Click"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	svc.HandleUnsubPOST(rr, req, unsubTok)
	if rr.Code != 200 || !unsubCalled {
		t.Fatalf("post: %d called=%v", rr.Code, unsubCalled)
	}

	rr = httptest.NewRecorder()
	svc.HandleOpen(rr, httptest.NewRequest(http.MethodGet, "/", nil), "bad.token")
	if rr.Code != 403 {
		t.Fatalf("bad token: %d", rr.Code)
	}
	_ = io.EOF
	_ = errs.CodeSignedURLInvalid
}

func TestApplyNoop(t *testing.T) {
	s := &Service{SignKey: []byte("k"), PublicURL: "https://eb.example"}
	res, err := s.Apply(context.Background(), PrepareInput{HTML: "<p>x</p>"})
	if err != nil || res.HTML != "<p>x</p>" {
		t.Fatalf("%+v %v", res, err)
	}
}
