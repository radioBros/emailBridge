package tracking

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/store"
)

func TestMapTokenErrAndHTMLEsc(t *testing.T) {
	if mapTokenErr(errBadToken) == nil || mapTokenErr(errExpired) == nil {
		t.Fatal("map")
	}
	if htmlEsc(`<&"`) != `&lt;&amp;&quot;` {
		t.Fatal(htmlEsc(`<&"`))
	}
	if itoa(0) != "0" || itoa(42) != "42" {
		t.Fatal(itoa(42))
	}
}

func TestHeaderGetCopy(t *testing.T) {
	h := copyHeaders(map[string]string{"A": "1"})
	h["B"] = "2"
	if _, ok := headerGet(h, "a"); !ok {
		t.Fatal("get")
	}
}

func TestWrongKindHandlers(t *testing.T) {
	key := []byte("test-tracking-sign-key-32-bytes!!")
	org := store.NewID(store.PrefixOrganization)
	out := store.NewID(store.PrefixOutbound)
	exp := time.Now().UTC().Add(time.Hour)
	svc := &Service{SignKey: key, Repo: nil}
	tok, _ := Sign(key, Claims{Kind: KindClick, OrgID: org, OutboundID: out, Recipient: "a@b.test", Exp: exp})
	rr := httptest.NewRecorder()
	svc.HandleOpen(rr, httptest.NewRequest(http.MethodGet, "/", nil), tok)
	if rr.Code != 403 {
		t.Fatal(rr.Code)
	}
	openTok, _ := Sign(key, Claims{Kind: KindOpen, OrgID: org, OutboundID: out, Recipient: "a@b.test", Exp: exp})
	rr = httptest.NewRecorder()
	svc.HandleClick(rr, httptest.NewRequest(http.MethodGet, "/", nil), openTok)
	if rr.Code != 403 {
		t.Fatal(rr.Code)
	}
	rr = httptest.NewRecorder()
	svc.HandleUnsubGET(rr, httptest.NewRequest(http.MethodGet, "/", nil), openTok)
	if rr.Code != 403 {
		t.Fatal(rr.Code)
	}
	rr = httptest.NewRecorder()
	svc.HandleUnsubPOST(rr, httptest.NewRequest(http.MethodPost, "/", nil), openTok)
	if rr.Code != 403 {
		t.Fatal(rr.Code)
	}
}

func TestSwitchStateDefaultOff(t *testing.T) {
	s := &Service{Registry: features.DefaultRegistry(), Env: "production", Repo: &Repo{}}
	o, c, u := s.SwitchState(context.Background(), store.NewID(store.PrefixOrganization), "")
	if o || c || u {
		t.Fatal("defaults off")
	}
}

func TestPreparePreservesExistingUnsubHeaders(t *testing.T) {
	key := []byte("test-tracking-sign-key-32-bytes!!")
	res, err := Prepare(key, PrepareInput{
		OrgID: store.NewID(store.PrefixOrganization), OutboundID: store.NewID(store.PrefixOutbound),
		Recipient: "a@b.test", ListUnsub: true, PublicURL: "https://eb.example",
		Headers: map[string]string{"List-Unsubscribe": "<mailto:u@x>"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Headers["List-Unsubscribe"] != "<mailto:u@x>" {
		t.Fatal(res.Headers)
	}
}

func TestVerifyMoreEdges(t *testing.T) {
	key := []byte("test-tracking-sign-key-32-bytes!!")
	if _, err := Verify(key, "abc", time.Now()); err == nil {
		t.Fatal("no dot")
	}
	if _, err := Verify(key, ".", time.Now()); err == nil {
		t.Fatal("empty parts")
	}
	tok, _ := Sign(key, Claims{
		Kind: KindOpen, OrgID: store.NewID(store.PrefixOrganization),
		OutboundID: store.NewID(store.PrefixOutbound), Recipient: "a@b.test",
		Exp: time.Now().UTC().Add(time.Hour),
	})
	parts := strings.SplitN(tok, ".", 2)
	// corrupt payload encoding but keep structure
	if _, err := Verify(key, "!!!."+parts[1], time.Now()); err == nil {
		t.Fatal("bad payload b64")
	}
	if mapTokenErr(context.Canceled) == nil {
		t.Fatal("wrap")
	}
}

func TestApplyNeedsPublicURL(t *testing.T) {
	s := &Service{SignKey: []byte("k")}
	_, err := s.Apply(context.Background(), PrepareInput{TrackOpens: true, HTML: "<p>x</p>"})
	if err == nil {
		t.Fatal("expected")
	}
}

func TestLikelyBotPurpose(t *testing.T) {
	r, _ := http.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("User-Agent", "Mozilla/5.0")
	r.Header.Set("Purpose", "prefetch")
	if !LikelyBot(r) {
		t.Fatal("prefetch")
	}
	r2, _ := http.NewRequest(http.MethodGet, "/", nil)
	if !LikelyBot(r2) {
		t.Fatal("empty ua")
	}
}
