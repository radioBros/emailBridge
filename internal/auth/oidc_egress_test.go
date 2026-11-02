package auth

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/config"
	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/errs"
)

func TestOIDCProviderRefusesPrivateIssuer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"issuer":"http://127.0.0.1","authorization_endpoint":"http://127.0.0.1/a","token_endpoint":"http://127.0.0.1/t","jwks_uri":"http://127.0.0.1/j"}`))
	}))
	t.Cleanup(srv.Close)
	s := &Service{
		Egress: egress.NewDialer(egress.Policy{BlockPrivate: true, BlockLinkLocal: true}),
		log:    discard(), security: discard(),
	}
	_, err := s.oidcProvider(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("want refuse loopback IdP")
	}
}

func TestOIDCProviderAllowsAllowListedPrivate(t *testing.T) {
	mock := newMockOIDC(t, "c", "s")
	host, port, err := net.SplitHostPort(mock.Server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	ip := netip.MustParseAddr(host)
	d := egress.NewDialer(egress.Policy{
		BlockPrivate: true, BlockLinkLocal: true,
		Allow: []netip.Prefix{netip.PrefixFrom(ip, ip.BitLen())},
	})
	s := &Service{Egress: d, log: discard(), security: discard()}
	issuer := "http://" + net.JoinHostPort(host, port)
	mock.issuer = issuer
	_, err = s.oidcProvider(context.Background(), issuer)
	if err != nil {
		t.Fatalf("allow-listed private IdP: %v", err)
	}
}

func TestOIDCHTTPClientDefaults(t *testing.T) {
	s := &Service{}
	if s.httpClient() == nil {
		t.Fatal("secure default")
	}
	s.HTTP = http.DefaultClient
	if s.httpClient() != http.DefaultClient {
		t.Fatal("override")
	}
	s.HTTP = nil
	s.Egress = egress.NewDialer(egress.Policy{})
	if s.httpClient() == nil {
		t.Fatal("egress client")
	}
}

func TestOIDCVerifyAndExchangeViaHTTP(t *testing.T) {
	mock := newMockOIDC(t, "cid", "sec")
	s := &Service{HTTP: http.DefaultClient, log: discard(), security: discard()}
	claims, err := s.verifyIDToken(context.Background(), mock.issuer, "cid", mustMint(t, mock, "n1", "a@b.c"), "n1")
	if err != nil || claims.Email != "a@b.c" {
		t.Fatalf("%v %+v", err, claims)
	}
	if _, err := s.verifyIDToken(context.Background(), mock.issuer, "cid", "not-a-jwt", "n"); err == nil {
		t.Fatal("bad token")
	}
	// exchange: put a code then redeem
	code := "unit-code"
	mock.mu.Lock()
	mock.codes[code] = mockCode{nonce: "n2", email: "x@y.z", redirect: "https://eb.example/cb", verifier: "", verified: true}
	mock.mu.Unlock()
	p := &OIDCProvider{IssuerURL: mock.issuer, ClientID: "cid", Scopes: []string{"openid"}}
	tok, err := s.exchangeOIDCCode(context.Background(), p, "sec", code, "", "https://eb.example/cb")
	if err != nil || tok == nil {
		t.Fatal(err)
	}
	if _, err := idTokenFromOauthToken(tok); err != nil {
		t.Fatal(err)
	}
}

func mustMint(t *testing.T, m *mockOIDC, nonce, email string) string {
	t.Helper()
	raw, err := m.mint(mockCode{nonce: nonce, email: email, verified: true})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestOIDCRateLimitFailClosed(t *testing.T) {
	kv := newFakeKV()
	kv.fail = errCacheDown
	s := &Service{kv: kv, log: discard(), security: discard(), o: Options{}}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	err := s.oidcRateLimit(context.Background(), r)
	if errs.CodeOf(err) != errs.CodeServiceUnavailable {
		t.Fatalf("got %v", err)
	}
	if err := s.oidcRateLimit(context.Background(), r); errs.CodeOf(err) != errs.CodeServiceUnavailable {
		t.Fatal(err)
	}
}

func TestOIDCRateLimitOverBudget(t *testing.T) {
	kv := newFakeKV()
	s := &Service{kv: kv, log: discard(), o: Options{LoginRate: config.Rate{Count: 2, Period: time.Minute}}}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "192.0.2.9:1"
	if err := s.oidcRateLimit(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if err := s.oidcRateLimit(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	if err := s.oidcRateLimit(context.Background(), r); errs.CodeOf(err) != errs.CodeRateLimited {
		t.Fatalf("got %v", err)
	}
}

func TestWarnRateLimitOnceNilSafe(t *testing.T) {
	var s *Service
	s.warnRateLimitOnce(context.Background(), "x", 0, errCacheDown)
	s = &Service{log: discard()}
	s.warnRateLimitOnce(context.Background(), "y", time.Millisecond, errCacheDown)
	s.warnRateLimitOnce(context.Background(), "y", time.Hour, errCacheDown) // suppressed
}

func TestCacheAddFailClosed(t *testing.T) {
	kv := newFakeKV()
	s := &Service{kv: kv, log: discard()}
	n, err := s.cacheAddFailClosed(context.Background(), "k", time.Minute, "otp-send")
	if err != nil || n != 1 {
		t.Fatal(n, err)
	}
	kv.fail = errCacheDown
	_, err = s.cacheAddFailClosed(context.Background(), "k2", time.Minute, "otp-send")
	if errs.CodeOf(err) != errs.CodeServiceUnavailable {
		t.Fatal(err)
	}
}

func TestAuthModuleMeta(t *testing.T) {
	m := &Module{}
	if m.Name() != "auth" || len(m.Roles()) == 0 {
		t.Fatal(m.Name(), m.Roles())
	}
	if err := m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
}

var errCacheDown = errString("cache down")

type errString string

func (e errString) Error() string { return string(e) }
