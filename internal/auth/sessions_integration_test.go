//go:build integration

package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestIntegrationOwnSessions(t *testing.T) {
	h := newHarness(t, func(o *Options) { o.Auth.MFARequired = false })
	email := "sessions@example.test"
	_ = h.user(email)

	c1 := h.client()
	if r := c1.signIn(email); r.status != http.StatusOK {
		t.Fatalf("sign-in 1 = %d %s", r.status, r.body)
	}
	me1 := c1.me()
	tok1 := c1.cookie(SessionCookie).Value

	c2 := h.client()
	c2.ip = "203.0.113.10"
	if r := c2.signIn(email); r.status != http.StatusOK {
		t.Fatalf("sign-in 2 = %d %s", r.status, r.body)
	}
	me2 := c2.me()
	if me1.SessionID == "" || me2.SessionID == "" || me1.SessionID == me2.SessionID {
		t.Fatalf("session ids = %q %q", me1.SessionID, me2.SessionID)
	}

	r := c1.do(http.MethodGet, "/v1/me/sessions", nil)
	if r.status != http.StatusOK {
		t.Fatalf("list = %d %s", r.status, r.body)
	}
	var listed struct {
		Data []SessionView `json:"data"`
	}
	if err := json.Unmarshal(r.body, &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Data) != 2 {
		t.Fatalf("want 2 sessions, got %d %s", len(listed.Data), r.body)
	}
	var cur, other *SessionView
	for i := range listed.Data {
		s := &listed.Data[i]
		if s.ID == me1.SessionID {
			cur = s
		}
		if s.ID == me2.SessionID {
			other = s
		}
		if strings.Contains(string(r.body), tok1) || strings.Contains(string(r.body), c2.cookie(SessionCookie).Value) {
			t.Fatalf("response leaked a token: %s", r.body)
		}
	}
	if cur == nil || !cur.Current || other == nil || other.Current {
		t.Fatalf("current flags: cur=%+v other=%+v", cur, other)
	}
	if cur.Method != MethodPassword || other.Method != MethodPassword {
		t.Fatalf("methods: %q %q", cur.Method, other.Method)
	}
	if other.IP == "" {
		t.Fatalf("expected IP on other session: %+v", other)
	}

	expectProblem(t, c1.do(http.MethodDelete, "/v1/me/sessions/not-a-session", nil), 404, "not_found")

	r = c1.do(http.MethodDelete, "/v1/me/sessions/"+me2.SessionID, nil)
	if r.status != http.StatusNoContent && r.status != http.StatusOK {
		t.Fatalf("revoke one = %d %s", r.status, r.body)
	}
	expectProblem(t, c2.do(http.MethodGet, "/v1/things", nil), 401, "unauthenticated")
	if r := c1.do(http.MethodGet, "/v1/things", nil); r.status != http.StatusOK {
		t.Fatalf("current still works = %d %s", r.status, r.body)
	}

	c3 := h.client()
	if r := c3.signIn(email); r.status != http.StatusOK {
		t.Fatal(r.status, r.body)
	}
	c4 := h.client()
	if r := c4.signIn(email); r.status != http.StatusOK {
		t.Fatal(r.status, r.body)
	}
	r = c1.do(http.MethodPost, "/v1/me/sessions/revoke-others", map[string]any{})
	if r.status != http.StatusOK {
		t.Fatalf("revoke-others = %d %s", r.status, r.body)
	}
	revoked, _ := r.json["revoked"].(float64)
	if revoked < 2 {
		t.Fatalf("revoked = %v, want ≥ 2; body %s", r.json["revoked"], r.body)
	}
	if r := c1.do(http.MethodGet, "/v1/things", nil); r.status != http.StatusOK {
		t.Fatalf("current after revoke-others = %d %s", r.status, r.body)
	}
	expectProblem(t, c3.do(http.MethodGet, "/v1/things", nil), 401, "unauthenticated")
	expectProblem(t, c4.do(http.MethodGet, "/v1/things", nil), 401, "unauthenticated")

	c1.hdr = map[string]string{"Authorization": "Bearer " + APIKeyPrefix + "testtoken"}
	expectProblem(t, c1.do(http.MethodGet, "/v1/me/sessions", nil), 403, "forbidden")
	c1.hdr = nil

	h2 := newHarness(t, nil)
	email2 := "mfa-sess@example.test"
	_ = h2.user(email2)
	cm := h2.client()
	if r := cm.signIn(email2); r.status != http.StatusOK {
		t.Fatal(r.status, r.body)
	}
	r = cm.do(http.MethodGet, "/v1/me/sessions", nil)
	if r.status != http.StatusOK {
		t.Fatalf("enrolment list = %d %s", r.status, r.body)
	}
	r = cm.do(http.MethodPost, "/v1/me/sessions/revoke-others", map[string]any{})
	if r.status != http.StatusOK {
		t.Fatalf("enrolment revoke-others = %d %s", r.status, r.body)
	}
}

func TestIntegrationOwnSessionsCachePurge(t *testing.T) {
	h := newHarness(t, func(o *Options) { o.Auth.MFARequired = false })
	email := "cache-sess@example.test"
	_ = h.user(email)
	c := h.client()
	if r := c.signIn(email); r.status != http.StatusOK {
		t.Fatal(r.status, r.body)
	}
	token := c.cookie(SessionCookie).Value
	me := c.me()
	ctx := context.Background()
	key := fmt.Sprintf("%s:session:t:%s", cacheKeyPrefix, token)
	if err := h.bus.CacheSet(ctx, key, []byte(`{"token":"`+token+`"}`), time.Hour); err != nil {
		t.Fatal(err)
	}
	r := c.do(http.MethodDelete, "/v1/me/sessions/"+me.SessionID, nil)
	if r.status != http.StatusNoContent && r.status != http.StatusOK {
		t.Fatalf("revoke = %d %s", r.status, r.body)
	}
	if _, err := h.bus.CacheGet(ctx, key); err == nil {
		t.Fatal("session cache entry still present after revoke")
	}
	expectProblem(t, c.do(http.MethodGet, "/v1/things", nil), 401, "unauthenticated")
}
