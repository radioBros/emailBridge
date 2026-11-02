package simulator

import (
	"testing"
)

func TestParseXOAuth2(t *testing.T) {
	user, tok, ok := parseXOAuth2([]byte("user=a@b.com\x01auth=Bearer tok\x01\x01"))
	if !ok || user != "a@b.com" || tok != "tok" {
		t.Fatalf("%v %v %v", user, tok, ok)
	}
	if _, _, ok := parseXOAuth2([]byte("nope")); ok {
		t.Fatal()
	}
}

func TestOAuthTokenRegistry(t *testing.T) {
	sim, err := New(Options{})
	if err != nil {
		t.Fatal(err)
	}
	if sim.hasOAuthTokens() {
		t.Fatal()
	}
	sim.SetOAuthToken("User@Ex.Com", "tok")
	if !sim.oauthTokenOK("user@ex.com", "tok") || !sim.hasOAuthTokens() {
		t.Fatal()
	}
	sim.ClearOAuthToken("user@ex.com")
	if sim.hasOAuthTokens() {
		t.Fatal()
	}
}
