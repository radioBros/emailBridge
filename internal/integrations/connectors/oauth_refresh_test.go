package connectors

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRefreshOAuthToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("refresh_token") != "rt" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"access_token":"at-new"}`))
	}))
	defer srv.Close()

	tok, err := RefreshOAuthToken(context.Background(), srv.Client(), OAuthTokenInput{
		TokenURL: srv.URL, ClientID: "c", ClientSecret: "s", RefreshToken: "rt", Provider: TypeGDrive,
	})
	if err != nil || tok != "at-new" {
		t.Fatalf("%q %v", tok, err)
	}
	tok, err = RefreshOAuthToken(context.Background(), nil, OAuthTokenInput{AccessToken: "cached"})
	if err != nil || tok != "cached" {
		t.Fatalf("%q %v", tok, err)
	}
	_, err = RefreshOAuthToken(context.Background(), srv.Client(), OAuthTokenInput{
		TokenURL: srv.URL, ClientID: "c", ClientSecret: "s", RefreshToken: "bad", Provider: TypeGDrive,
	})
	if err == nil || !strings.Contains(err.Error(), "HTTP") {
		t.Fatal(err)
	}
}
