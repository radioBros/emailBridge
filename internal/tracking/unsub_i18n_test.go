package tracking

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/store"
)

func TestUnsubPagesLocalised(t *testing.T) {
	key := []byte("test-tracking-sign-key-32-bytes!!")
	org := store.NewID(store.PrefixOrganization)
	out := store.NewID(store.PrefixOutbound)
	exp := time.Now().UTC().Add(time.Hour)
	svc := &Service{
		SignKey: key, PublicURL: "https://eb.example", TTL: time.Hour, Repo: nil,
		Now: func() time.Time { return time.Now().UTC() },
	}
	tok, err := Sign(key, Claims{Kind: KindUnsub, OrgID: org, OutboundID: out, Recipient: "user@example.test", Exp: exp})
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/u/"+tok, nil)
	req.Header.Set("Accept-Language", "ru")
	svc.HandleUnsubGET(rr, req, tok)
	body := rr.Body.String()
	if rr.Code != 200 || !strings.Contains(body, `lang="ru"`) || !strings.Contains(body, "Отписаться") {
		t.Fatalf("ru get: %s", body)
	}
	if !strings.Contains(body, "viewport") {
		t.Fatal("missing viewport")
	}

	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/u/"+tok, nil)
	req.Header.Set("Accept-Language", "xx-YY,zz;q=0.8")
	svc.HandleUnsubGET(rr, req, tok)
	body = rr.Body.String()
	if rr.Code != 200 || !strings.Contains(body, `lang="en"`) || !strings.Contains(body, "Unsubscribe") {
		t.Fatalf("unknown → en: %s", body)
	}

	called := false
	svc.Unsub = func(_ context.Context, o, addr, _, _ string) error {
		called = true
		if o != org || addr != "user@example.test" {
			t.Fatalf("%s %s", o, addr)
		}
		return nil
	}
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/u/"+tok, strings.NewReader("List-Unsubscribe=One-Click"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept-Language", "ru")
	svc.HandleUnsubPOST(rr, req, tok)
	body = rr.Body.String()
	if rr.Code != 200 || !called || !strings.Contains(body, "отписан") {
		t.Fatalf("ru post: code=%d called=%v body=%s", rr.Code, called, body)
	}
}
