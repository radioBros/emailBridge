package oauth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
)

func TestFetchGmailSendAs(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"sendAs": []map[string]any{{
				"sendAsEmail": "a@ex.com", "signature": "sig", "isDefault": true,
			}},
		})
	}))
	defer srv.Close()
	list, err := fetchGmailSendAs(context.Background(), srv.Client(), srv.URL, "", "tok")
	if err != nil || len(list) != 1 || list[0].Signature != "sig" {
		t.Fatalf("%v %#v", err, list)
	}
	_, err = fetchGmailSendAs(context.Background(), srv.Client(), srv.URL, "a@ex.com", "bad")
	if errs.CodeOf(err) != errs.CodeOauthRevoked {
		t.Fatalf("%v", err)
	}
}

func TestTokenExportRate(t *testing.T) {
	t.Parallel()
	s := &Service{now: func() time.Time { return time.Unix(1000, 0).UTC() }}
	id := "acc_rate_" + t.Name()
	for i := 0; i < tokenExportRatePerHour; i++ {
		if err := s.takeTokenExportRate(id); err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
	}
	if errs.CodeOf(s.takeTokenExportRate(id)) != errs.CodeRateLimited {
		t.Fatal("expected rate limited")
	}
}

func TestGmailAPIRoot(t *testing.T) {
	t.Parallel()
	s := &Service{}
	if s.gmailAPIRoot() != "https://gmail.googleapis.com" {
		t.Fatal(s.gmailAPIRoot())
	}
	s.GmailAPIRoot = "http://localhost/"
	if s.gmailAPIRoot() != "http://localhost" {
		t.Fatal(s.gmailAPIRoot())
	}
}
