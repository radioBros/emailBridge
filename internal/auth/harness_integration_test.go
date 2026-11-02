//go:build integration

package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/netip"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/descope/virtualwebauthn"
	"github.com/pquerna/otp/totp"

	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/config"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/store"
)

const testPassword = "correct horse battery staple"

// captureMailer records the mails the auth layer enqueues.
type captureMailer struct {
	mu    sync.Mutex
	mails []Mail
	err   error
}

func (c *captureMailer) Enqueue(_ context.Context, m Mail) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.mails = append(c.mails, m)
	return c.err
}

func (c *captureMailer) last(kind string) (Mail, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	var out Mail
	for _, m := range c.mails {
		if m.Kind == kind {
			out = m
			n++
		}
	}
	return out, n
}

type harness struct {
	t      *testing.T
	base   string
	svc    *Service
	db     *store.DB
	bus    *bus.Bus
	mailer *captureMailer
}

func testStore(t *testing.T) *store.DB {
	t.Helper()
	uri := os.Getenv("EB_TEST_MONGO_URI")
	if uri == "" {
		uri = "mongodb://127.0.0.1:27017/?replicaSet=rs0&directConnection=true"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	db, err := store.Connect(ctx, store.Options{URI: uri, Database: fmt.Sprintf("ebauth_%d", time.Now().UnixNano()), ConnectTimeout: 5 * time.Second})
	if err != nil {
		t.Skipf("no MongoDB replica set at %s: %v", uri, err)
	}
	t.Cleanup(func() {
		_ = db.DB.Drop(context.Background())
		_ = db.Close(context.Background())
	})
	return db
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// newHarness starts MongoDB-backed Limen behind the real httpapi server on http://localhost:<port>
// (a WebAuthn RP ID cannot be an IP). tweak adjusts the options before the service is built.
func newHarness(t *testing.T, tweak func(*Options)) *harness {
	t.Helper()
	db := testStore(t)
	b, err := bus.Open(context.Background(), bus.Options{Mode: bus.ModeEmbedded, NodeID: "t", DataDir: t.TempDir(), Log: quiet()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	base := fmt.Sprintf("http://localhost:%d", ln.Addr().(*net.TCPAddr).Port)
	srv, err := httpapi.New(httpapi.Options{
		PublicURL:      base,
		Env:            "development",
		TrustedProxies: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")},
		AccessLog:      quiet(),
		SecurityLog:    quiet(),
	})
	if err != nil {
		t.Fatal(err)
	}
	m := &captureMailer{}
	o := Options{
		Auth: config.AuthConfig{
			Secret: "0123456789abcdef0123456789abcdef", CookieSecure: false, SessionTTL: 12 * time.Hour, SessionIdle: 2 * time.Hour,
			MFARequired: true, WebAuthnRPID: "localhost", LockoutThreshold: 5, LockoutDuration: time.Minute,
		},
		PublicURL: base,
		Env:       "development",
		LoginRate: config.Rate{Count: 1000, Period: time.Minute},
		EmailOTP:  true,
		Mailer:    m,
		Log:       quiet(),
	}
	if tweak != nil {
		tweak(&o)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	svc, err := New(ctx, o, db.DB, b)
	if err != nil {
		t.Fatal(err)
	}
	svc.Mount(srv)
	srv.Router.Get("/v1/me", func(w http.ResponseWriter, r *http.Request) {
		id, err := Require(r.Context())
		if err != nil {
			writeErr(w, r, err)
			return
		}
		_ = json.NewEncoder(w).Encode(id)
	})
	srv.Router.With(RequireSession).Get("/v1/things", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	hs := &http.Server{Handler: srv, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = hs.Serve(ln) }()
	t.Cleanup(func() { _ = hs.Close() })
	return &harness{t: t, base: base, svc: svc, db: db, bus: b, mailer: m}
}

func (h *harness) user(email string) string {
	h.t.Helper()
	id, err := h.svc.CreateUser(context.Background(), email, testPassword)
	if err != nil {
		h.t.Fatal(err)
	}
	return id
}

type client struct {
	h   *harness
	c   *http.Client
	ip  string // X-Forwarded-For (trusted proxy 127.0.0.1)
	hdr map[string]string
}

func (h *harness) client() *client {
	jar, _ := cookiejar.New(nil)
	return &client{h: h, c: &http.Client{Jar: jar, Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}

type resp struct {
	status int
	header http.Header
	body   []byte
	json   map[string]any
}

func (r resp) str(k string) string { s, _ := r.json[k].(string); return s }

func (c *client) do(method, path string, body any) resp {
	c.h.t.Helper()
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = strings.NewReader(b)
	default:
		raw, _ := json.Marshal(b)
		rd = bytes.NewReader(raw)
	}
	req, _ := http.NewRequest(method, c.h.base+path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Origin", c.h.base)
	req.Header.Set("X-Requested-With", "EmailBridge")
	if c.ip != "" {
		req.Header.Set("X-Forwarded-For", c.ip)
	}
	for k, v := range c.hdr {
		req.Header.Set(k, v)
	}
	res, err := c.c.Do(req)
	if err != nil {
		c.h.t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, _ := io.ReadAll(res.Body)
	out := resp{status: res.StatusCode, header: res.Header, body: raw}
	_ = json.Unmarshal(raw, &out.json)
	return out
}

func (c *client) cookie(name string) *http.Cookie {
	u, _ := url.Parse(c.h.base)
	for _, ck := range c.c.Jar.Cookies(u) {
		if ck.Name == name {
			return ck
		}
	}
	return nil
}

func (c *client) signIn(email string) resp {
	c.h.t.Helper()
	return c.do(http.MethodPost, "/v1/auth/signin/credential", map[string]any{"credential": email, "password": testPassword})
}

func (c *client) me() Identity {
	c.h.t.Helper()
	r := c.do(http.MethodGet, "/v1/me", nil)
	if r.status != http.StatusOK {
		c.h.t.Fatalf("GET /v1/me = %d %s", r.status, r.body)
	}
	var id Identity
	_ = json.Unmarshal(r.body, &id)
	return id
}

// enrolTOTP signs the client's (password) session up for TOTP and returns the secret and the code that
// finalized enrolment (already claimed for replay protection — callers must wait for a later step).
func (c *client) enrolTOTP() (secret, usedCode string) {
	c.h.t.Helper()
	r := c.do(http.MethodPost, "/v1/auth/two-factor/initiate-setup", map[string]any{"password": testPassword})
	if r.status != http.StatusOK {
		c.h.t.Fatalf("initiate-setup = %d %s", r.status, r.body)
	}
	u, err := url.Parse(r.str("uri"))
	if err != nil {
		c.h.t.Fatal(err)
	}
	secret = u.Query().Get("secret")
	usedCode, _ = totp.GenerateCode(secret, time.Now())
	r = c.do(http.MethodPost, "/v1/auth/two-factor/finalize-setup", map[string]any{"code": usedCode})
	if r.status != http.StatusOK {
		c.h.t.Fatalf("finalize-setup = %d %s", r.status, r.body)
	}
	return secret, usedCode
}

// freshTOTP waits until the TOTP step rolls past every code in used (enrolment and prior verifies claim
// codes for 3 minutes, so the same digits must not be reused within the window).
func freshTOTP(t *testing.T, secret string, used ...string) string {
	t.Helper()
	seen := map[string]bool{}
	for _, c := range used {
		seen[c] = true
	}
	deadline := time.Now().Add(45 * time.Second)
	for {
		code, err := totp.GenerateCode(secret, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if !seen[code] {
			return code
		}
		if time.Now().After(deadline) {
			t.Fatal("TOTP step did not advance")
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func expectProblem(t *testing.T, r resp, status int, code string) {
	t.Helper()
	if r.status != status || r.str("code") != code || !strings.HasPrefix(r.header.Get("Content-Type"), "application/problem+json") {
		t.Fatalf("want %d %s problem, got %d %s %s", status, code, r.status, r.header.Get("Content-Type"), r.body)
	}
}

// passkeyDevice is a virtual authenticator with one credential.
type passkeyDevice struct {
	rp   virtualwebauthn.RelyingParty
	auth virtualwebauthn.Authenticator
	cred virtualwebauthn.Credential
}

func newDevice(h *harness) *passkeyDevice {
	return &passkeyDevice{
		rp:   virtualwebauthn.RelyingParty{Name: "EmailBridge", ID: "localhost", Origin: h.base},
		auth: virtualwebauthn.NewAuthenticator(),
		cred: virtualwebauthn.NewCredential(virtualwebauthn.KeyTypeEC2),
	}
}

func (c *client) registerPasskey(d *passkeyDevice) resp {
	c.h.t.Helper()
	r := c.do(http.MethodGet, "/v1/auth/passkeys/begin-registration", nil)
	if r.status != http.StatusOK {
		c.h.t.Fatalf("begin-registration = %d %s", r.status, r.body)
	}
	opts, err := virtualwebauthn.ParseAttestationOptions(unwrap(r.body))
	if err != nil {
		c.h.t.Fatal(err)
	}
	d.auth.Options.UserHandle = []byte(opts.UserID)
	att := virtualwebauthn.CreateAttestationResponse(d.rp, d.auth, d.cred, *opts)
	d.auth.AddCredential(d.cred)
	return c.do(http.MethodPost, "/v1/auth/passkeys/finish-registration", att)
}

func (c *client) passkeySignIn(d *passkeyDevice) resp {
	c.h.t.Helper()
	r := c.do(http.MethodGet, "/v1/auth/passkeys/begin-authentication", nil)
	if r.status != http.StatusOK {
		c.h.t.Fatalf("begin-authentication = %d %s", r.status, r.body)
	}
	opts, err := virtualwebauthn.ParseAssertionOptions(unwrap(r.body))
	if err != nil {
		c.h.t.Fatal(err)
	}
	return c.do(http.MethodPost, "/v1/auth/passkeys/finish-authentication", virtualwebauthn.CreateAssertionResponse(d.rp, d.auth, d.cred, *opts))
}

// unwrap returns the "data" member when Limen wraps a payload, else the body.
func unwrap(raw []byte) string {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) == nil {
		if d, ok := m["data"]; ok {
			return string(d)
		}
	}
	return string(raw)
}

func mustURL(s string) *url.URL {
	u, err := url.Parse(s)
	if err != nil {
		panic(err)
	}
	return u
}
