//go:build integration

package auth

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// The GDPR export (S11 §6) lists sessions without tokens and marks the current one.
func TestIntegrationExportSessionsAndPasskeys(t *testing.T) {
	h := newHarness(t, func(o *Options) { o.Auth.MFARequired = false })
	email := "export@example.test"
	authUserID := h.user(email)

	c1 := h.client()
	if r := c1.signIn(email); r.status != http.StatusOK {
		t.Fatalf("sign-in = %d %s", r.status, r.body)
	}
	me := c1.me()
	tok := c1.cookie(SessionCookie).Value
	c2 := h.client()
	if r := c2.signIn(email); r.status != http.StatusOK {
		t.Fatalf("sign-in 2 = %d %s", r.status, r.body)
	}

	got, err := h.svc.ExportSessions(context.Background(), authUserID, me.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("sessions = %d, want 2", len(got))
	}
	current := 0
	for _, s := range got {
		if s.Current {
			current++
			if s.ID != me.SessionID {
				t.Fatalf("current = %s, want %s", s.ID, me.SessionID)
			}
		}
		if strings.Contains(s.ID, tok) || s.ID == "" || s.CreatedAt.IsZero() {
			t.Fatalf("session view leaks the token or is empty: %+v", s)
		}
	}
	if current != 1 {
		t.Fatalf("current sessions = %d", current)
	}

	keys, err := h.svc.ExportPasskeys(context.Background(), authUserID)
	if err != nil || len(keys) != 0 {
		t.Fatalf("passkeys = %v %v", keys, err)
	}
}
