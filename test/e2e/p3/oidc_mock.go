//go:build e2e

package p3

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// mockOIDC is an in-process OIDC provider (discovery, JWKS, authorize, token) for e2e.
type mockOIDC struct {
	t         *testing.T
	issuer    string
	clientID  string
	clientSec string
	key       *ecdsa.PrivateKey
	signer    jose.Signer
	mu        sync.Mutex
	codes     map[string]mockCode
	Server    *httptest.Server
	Email     string
}

type mockCode struct {
	nonce, email, redirect, challenge string
}

func newMockOIDC(t *testing.T, clientID, clientSec string) *mockOIDC {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.ES256, Key: key}, (&jose.SignerOptions{}).WithType("JWT"))
	if err != nil {
		t.Fatal(err)
	}
	m := &mockOIDC{
		t: t, clientID: clientID, clientSec: clientSec, key: key, signer: signer,
		codes: map[string]mockCode{}, Email: "sso@e2e.test",
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", m.discovery)
	mux.HandleFunc("/jwks", m.jwks)
	mux.HandleFunc("/authorize", m.authorize)
	mux.HandleFunc("/token", m.token)
	m.Server = httptest.NewServer(mux)
	m.issuer = m.Server.URL
	t.Cleanup(m.Server.Close)
	return m
}

func (m *mockOIDC) discovery(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"issuer":                                m.issuer,
		"authorization_endpoint":                m.issuer + "/authorize",
		"token_endpoint":                        m.issuer + "/token",
		"jwks_uri":                              m.issuer + "/jwks",
		"response_types_supported":              []string{"code"},
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{"ES256"},
	})
}

func (m *mockOIDC) jwks(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	pub := m.key.PublicKey
	jwk := jose.JSONWebKey{Key: &pub, Algorithm: string(jose.ES256), Use: "sig", KeyID: "e2e"}
	_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{jwk}})
}

func (m *mockOIDC) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	state, redir := q.Get("state"), q.Get("redirect_uri")
	nonce, challenge := q.Get("nonce"), q.Get("code_challenge")
	email := q.Get("login_hint")
	if email == "" {
		email = m.Email
	}
	code := "code-" + base64.RawURLEncoding.EncodeToString([]byte(state))
	if len(code) > 24 {
		code = code[:24]
	}
	m.mu.Lock()
	m.codes[code] = mockCode{nonce: nonce, email: email, redirect: redir, challenge: challenge}
	m.mu.Unlock()
	u, _ := url.Parse(redir)
	qq := u.Query()
	qq.Set("code", code)
	qq.Set("state", state)
	u.RawQuery = qq.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound) //nolint:gosec // G710: test mock redirects to the authorize redirect_uri under test
}

func (m *mockOIDC) token(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	id, secret, _ := r.BasicAuth()
	if id == "" {
		id, secret = r.Form.Get("client_id"), r.Form.Get("client_secret")
	}
	if id != m.clientID || secret != m.clientSec {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	code := r.Form.Get("code")
	m.mu.Lock()
	mc, ok := m.codes[code]
	delete(m.codes, code)
	m.mu.Unlock()
	if !ok {
		http.Error(w, "invalid_grant", http.StatusBadRequest)
		return
	}
	if mc.challenge != "" {
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		got := base64.RawURLEncoding.EncodeToString(sum[:])
		if got != mc.challenge {
			http.Error(w, "invalid_grant pkce", http.StatusBadRequest)
			return
		}
	}
	idTok, err := m.mint(mc)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"access_token": "at", "token_type": "Bearer", "expires_in": 3600, "id_token": idTok,
	})
}

func (m *mockOIDC) mint(mc mockCode) (string, error) {
	now := time.Now()
	claims := map[string]any{
		"iss": m.issuer, "sub": "e2e-sub", "aud": m.clientID,
		"exp": now.Add(time.Hour).Unix(), "iat": now.Unix(), "nonce": mc.nonce,
		"email": mc.email, "email_verified": true,
	}
	return jwt.Signed(m.signer).Claims(claims).Serialize()
}
