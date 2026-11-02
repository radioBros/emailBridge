package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/thecodearcher/limen"

	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/config"
	"github.com/radiobros/emailbridge/internal/errs"
)

func discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// fakeKV is an in-memory KV with the bus semantics.
type fakeKV struct {
	mu   sync.Mutex
	m    map[string]bus.CacheEntry
	fail error
}

func newFakeKV() *fakeKV { return &fakeKV{m: map[string]bus.CacheEntry{}} }

func (f *fakeKV) get(key string) (bus.CacheEntry, bool) {
	e, ok := f.m[key]
	if ok && !e.ExpiresAt.IsZero() && !time.Now().Before(e.ExpiresAt) {
		delete(f.m, key)
		return bus.CacheEntry{}, false
	}
	return e, ok
}

func exp(ttl time.Duration) time.Time {
	if ttl <= 0 {
		return time.Time{}
	}
	return time.Now().Add(ttl)
}

func (f *fakeKV) CacheGet(_ context.Context, key string) (bus.CacheEntry, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return bus.CacheEntry{}, f.fail
	}
	e, ok := f.get(key)
	if !ok {
		return bus.CacheEntry{}, bus.ErrCacheMiss
	}
	return e, nil
}

func (f *fakeKV) CacheSet(_ context.Context, key string, v []byte, ttl time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return f.fail
	}
	f.m[key] = bus.CacheEntry{Value: v, ExpiresAt: exp(ttl)}
	return nil
}

func (f *fakeKV) CacheDelete(_ context.Context, key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return f.fail
	}
	delete(f.m, key)
	return nil
}

func (f *fakeKV) CacheAdd(_ context.Context, key string, d int64, ttl time.Duration) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return 0, f.fail
	}
	e, ok := f.get(key)
	var n int64
	if ok {
		n, _ = strconv.ParseInt(string(e.Value), 10, 64)
	} else {
		e.ExpiresAt = exp(ttl)
	}
	n += d
	e.Value = []byte(strconv.FormatInt(n, 10))
	f.m[key] = e
	return n, nil
}

func (f *fakeKV) CacheExpire(_ context.Context, key string, ttl time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return f.fail
	}
	e, ok := f.get(key)
	if !ok {
		return bus.ErrCacheMiss
	}
	e.ExpiresAt = exp(ttl)
	f.m[key] = e
	return nil
}

func TestLimenCacheAdapter(t *testing.T) {
	kv := newFakeKV()
	c := &limenCache{kv: kv, log: discard()}
	ctx := context.Background()
	if _, err := c.Get(ctx, "k"); !errors.Is(err, limen.ErrRecordNotFound) {
		t.Fatalf("miss = %v", err)
	}
	if ok, err := c.Has(ctx, "k"); ok || err != nil {
		t.Fatal("has miss")
	}
	if err := c.Set(ctx, "k", []byte("v"), time.Minute); err != nil {
		t.Fatal(err)
	}
	if v, err := c.Get(ctx, "k"); err != nil || string(v) != "v" {
		t.Fatal("get")
	}
	if ok, _ := c.Has(ctx, "k"); !ok {
		t.Fatal("has")
	}
	if err := c.SetExpiry(ctx, "k", time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := c.SetExpiry(ctx, "none", time.Hour); !errors.Is(err, limen.ErrRecordNotFound) {
		t.Fatalf("expire miss = %v", err)
	}
	if n, _ := c.Increment(ctx, "n", 3); n != 3 {
		t.Fatal("increment")
	}
	if n, _ := c.Decrement(ctx, "n", 1); n != 2 {
		t.Fatal("decrement")
	}
	if err := c.Delete(ctx, "k"); err != nil {
		t.Fatal(err)
	}
	kv.fail = errors.New("down")
	if _, err := c.Get(ctx, "k"); err == nil || errors.Is(err, limen.ErrRecordNotFound) {
		t.Fatal("error passthrough")
	}
	if _, err := c.Has(ctx, "k"); err == nil {
		t.Fatal("has error")
	}
	for _, err := range []error{c.Set(ctx, "k", nil, 0), c.Delete(ctx, "k"), c.SetExpiry(ctx, "k", 0)} {
		if err == nil {
			t.Fatal("error expected")
		}
	}
	if _, err := c.Increment(ctx, "n", 1); err == nil {
		t.Fatal("increment error")
	}
	if _, err := c.Decrement(ctx, "n", 1); err == nil {
		t.Fatal("decrement error")
	}
}

func TestProblemCode(t *testing.T) {
	cases := []struct {
		status int
		path   string
		msg    string
		want   errs.Code
	}{
		{401, "/signin/credential", "invalid credential", errs.CodeInvalidCredentials},
		{401, "/two-factor/verify", "something", errs.CodeMFAInvalid},
		{401, "/two-factor/verify", "invalid code", errs.CodeMFAInvalid},
		{401, "/passkeys/finish-authentication", "x", errs.CodePasskeyFailed},
		{401, "/signin/credential", "x", errs.CodeInvalidCredentials},
		{401, "/sessions", "session not found", errs.CodeUnauthenticated},
		{401, "/sessions", "session has expired", errs.CodeSessionExpired},
		{400, "/passkeys/finish-registration", "x", errs.CodePasskeyFailed},
		{400, "/x", "x", errs.CodeMalformedRequest},
		{403, "/x", "Origin not allowed", errs.CodeOriginRejected},
		{403, "/x", "Forbidden", errs.CodeCSRFFailed},
		{403, "/x", "email is not verified", errs.CodeForbidden},
		{404, "/x", "", errs.CodeNotFound},
		{405, "/x", "", errs.CodeMalformedRequest},
		{409, "/x", "", errs.CodeConflict},
		{413, "/x", "", errs.CodePayloadTooLarge},
		{415, "/x", "", errs.CodeUnsupportedMediaType},
		{422, "/x", "password is too short", errs.CodeValidationFailed},
		{429, "/x", "", errs.CodeRateLimited},
		{503, "/x", "", errs.CodeServiceUnavailable},
		{500, "/x", "", errs.CodeInternalError},
		{418, "/x", "", errs.CodeMalformedRequest},
	}
	for _, c := range cases {
		if got := problemCode(c.status, c.path, c.msg); got != c.want {
			t.Errorf("problemCode(%d, %s, %q) = %s, want %s", c.status, c.path, c.msg, got, c.want)
		}
	}
	if sentence("invalid code") != "Invalid code." || sentence("Done.") != "Done." || sentence("") != "" || sentence("404 page not found\n") != "" {
		t.Fatal("sentence")
	}
}

func TestTranslate(t *testing.T) {
	s := &Service{log: discard()}
	h := s.translate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/auth/ok":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"user":{}}`))
		case "/v1/auth/fail":
			http.SetCookie(w, &http.Cookie{Name: "x", Value: "y"})
			w.Header().Set("Retry-After", "7")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"message":"Too many requests. Please try again later."}`))
		case "/v1/auth/boom":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"message":"mongo: connection refused at 10.0.0.1"}`))
		case "/v1/auth/problem":
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusLocked)
			_, _ = w.Write([]byte(`{"code":"account_locked"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	do := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec
	}
	if rec := do("/v1/auth/ok"); rec.Code != 200 || rec.Body.String() != `{"user":{}}` {
		t.Fatalf("passthrough = %d %s", rec.Code, rec.Body)
	}
	rec := do("/v1/auth/fail")
	var p errs.Problem
	_ = json.Unmarshal(rec.Body.Bytes(), &p)
	if rec.Code != 429 || p.Code != errs.CodeRateLimited || rec.Header().Get("Retry-After") != "7" || !strings.Contains(rec.Header().Get("Set-Cookie"), "x=y") {
		t.Fatalf("translated = %d %s %v", rec.Code, rec.Body, rec.Header())
	}
	rec = do("/v1/auth/boom")
	if rec.Code != 500 || strings.Contains(rec.Body.String(), "10.0.0.1") {
		t.Fatalf("5xx must not leak: %s", rec.Body)
	}
	if rec := do("/v1/auth/problem"); rec.Code != 423 || rec.Body.String() != `{"code":"account_locked"}` {
		t.Fatalf("problem passthrough = %s", rec.Body)
	}
	rec = do("/v1/auth/missing")
	_ = json.Unmarshal(rec.Body.Bytes(), &p)
	if rec.Code != 404 || p.Code != errs.CodeNotFound || p.Detail == "404 page not found." {
		t.Fatalf("plain 404 = %d %s", rec.Code, rec.Body)
	}
	// writes without WriteHeader and repeated WriteHeader
	pw := &problemWriter{ResponseWriter: httptest.NewRecorder()}
	_, _ = pw.Write([]byte("x"))
	pw.WriteHeader(http.StatusInternalServerError)
	if pw.status != 200 || pw.Unwrap() == nil {
		t.Fatal("problemWriter")
	}
}

func TestSessionsHelpers(t *testing.T) {
	if !looksLikeAPIKey("Bearer "+APIKeyPrefix+"x") || looksLikeAPIKey("Bearer other") || looksLikeAPIKey("") {
		t.Fatal("looksLikeAPIKey")
	}
	if !sessionsPathAllowed(http.MethodGet, "/v1/me/sessions/") ||
		!sessionsPathAllowed(http.MethodDelete, "/v1/me/sessions/abc") ||
		!sessionsPathAllowed(http.MethodPost, "/v1/me/sessions/revoke-others") ||
		sessionsPathAllowed(http.MethodPatch, "/v1/me/sessions") {
		t.Fatal("sessionsPathAllowed")
	}
	if _, err := requireUserSession(context.Background(), "Bearer "+APIKeyPrefix+"x"); errs.CodeOf(err) != errs.CodeForbidden {
		t.Fatal(err)
	}
	if _, err := requireUserSession(context.Background(), ""); errs.CodeOf(err) != errs.CodeUnauthenticated {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	v := viewSession(limen.Session{
		ID: "sid", Token: "secret-token", CreatedAt: now, LastAccess: now, ExpiresAt: now.Add(time.Hour),
		Metadata: map[string]any{"user_agent": "UA", "ip_address": "1.2.3.4"},
	}, "sid")
	if !v.Current || v.Method != MethodUnknown || v.ID != "sid" || v.UserAgent != "UA" || v.IP != "1.2.3.4" {
		t.Fatalf("%+v", v)
	}
	s := &Service{log: discard()}
	if s.purgeSessionCache(context.Background(), "u", "") != nil || s.purgeSessionCache(context.Background(), "u", "tok") != nil {
		t.Fatal("no cache configured: nothing to purge")
	}
}

func TestIdentityHelpersAndGate(t *testing.T) {
	ctx := context.Background()
	if _, err := Require(ctx); errs.CodeOf(err) != errs.CodeUnauthenticated {
		t.Fatal("no identity")
	}
	if _, err := Require(context.WithValue(ctx, sessionStateKey{}, sessionState{expired: true})); errs.CodeOf(err) != errs.CodeSessionExpired {
		t.Fatal("expired")
	}
	idCtx := WithIdentity(ctx, Identity{AuthUserID: "u", MFA: false, Method: MethodPassword})
	if id, err := Require(idCtx); err != nil || id.AuthUserID != "u" {
		t.Fatal("identity")
	}
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	rec := httptest.NewRecorder()
	RequireSession(ok).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/x", nil))
	if rec.Code != 401 {
		t.Fatal("RequireSession without identity")
	}
	rec = httptest.NewRecorder()
	RequireSession(ok).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/x", nil).WithContext(idCtx))
	if rec.Code != 204 {
		t.Fatal("RequireSession with identity")
	}
	rec = httptest.NewRecorder()
	writeErr(rec, httptest.NewRequest(http.MethodGet, "/", nil), errors.New("plain"))
	if rec.Code != 500 {
		t.Fatal("writeErr plain")
	}

	factor := map[string]bool{"has": true}
	var factorErr error
	s := &Service{o: Options{Auth: config.AuthConfig{MFARequired: true}}, log: discard(), security: discard(),
		hasMFA: func(_ context.Context, id string) (bool, error) { return factor[id], factorErr }}
	gate := s.MFAGate(ok)
	withFactor := WithIdentity(ctx, Identity{AuthUserID: "has", Method: MethodPassword})
	for path, want := range map[string]int{"/v1/auth/passkeys/begin-registration": 401, "/v1/me": 204} {
		rec := httptest.NewRecorder()
		gate.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil).WithContext(withFactor))
		if rec.Code != want {
			t.Errorf("user with a factor: GET %s = %d, want %d", path, rec.Code, want)
		}
	}
	factorErr = errors.New("db down")
	rec = httptest.NewRecorder()
	gate.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/auth/passkeys/begin-registration", nil).WithContext(withFactor))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("check failure = %d", rec.Code)
	}
	factorErr = nil
	cases := []struct {
		method, path string
		ctx          context.Context
		want         int
	}{
		{"GET", "/v1/things", ctx, 204},
		{"GET", "/v1/things", idCtx, 401},
		{"GET", "/v1/me", idCtx, 204},
		{"GET", "/v1/me/", idCtx, 204},
		{"GET", "/v1/me/sessions", idCtx, 204},
		{"DELETE", "/v1/me/sessions/abc", idCtx, 204},
		{"POST", "/v1/me/sessions/revoke-others", idCtx, 204},
		{"POST", "/v1/auth/two-factor/initiate-setup", idCtx, 204},
		{"GET", "/v1/auth/passkeys/begin-registration", idCtx, 204},
		{"POST", "/v1/auth/signout", idCtx, 204},
		{"POST", "/v1/auth/two-factor/disable", idCtx, 401},
		{"GET", "/v1/things", WithIdentity(ctx, Identity{MFA: true}), 204},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		gate.ServeHTTP(rec, httptest.NewRequest(c.method, c.path, nil).WithContext(c.ctx))
		if rec.Code != c.want {
			t.Errorf("%s %s = %d, want %d", c.method, c.path, rec.Code, c.want)
		}
	}
	s.o.Auth.MFARequired = false
	rec = httptest.NewRecorder()
	s.MFAGate(ok).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/things", nil).WithContext(idCtx))
	if rec.Code != 204 {
		t.Fatal("gate off")
	}
	for m, want := range map[string]bool{MethodPassword: false, MethodUnknown: false, MethodPasskey: true, MethodPasswordTOTP: true, MethodPasswordOTP: true, MethodPasswordBackup: true, MethodPasswordPasskey: true} {
		if mfaMethod(m) != want {
			t.Errorf("mfaMethod(%s)", m)
		}
	}
}

func TestMethodFor(t *testing.T) {
	cur := Identity{Method: MethodPasskey}
	cases := []struct {
		path string
		body map[string]any
		has  bool
		want string
	}{
		{"/signin/credential", nil, false, MethodPassword},
		{"/two-factor/verify", map[string]any{"code": "abcde-fghij"}, false, MethodPasswordBackup},
		{"/two-factor/verify", map[string]any{"code": "123456", "method": "otp"}, false, MethodPasswordOTP},
		{"/two-factor/verify", map[string]any{"code": "123456", "method": " otp"}, false, MethodPasswordOTP},
		{"/two-factor/verify", map[string]any{"code": "123456"}, false, MethodPasswordTOTP},
		{"/two-factor/verify", map[string]any{"code": " 123456 ", "method": " totp"}, false, MethodPasswordTOTP},
		{"/two-factor/finalize-setup", nil, true, MethodPasswordTOTP},
		{"/passkeys/finish-authentication", nil, false, MethodPasskey},
		{"/passwords/change", nil, true, MethodPasskey},
		{"/passwords/change", nil, false, MethodUnknown},
	}
	for _, c := range cases {
		if got := methodFor(c.path, c.body, cur, c.has); got != c.want {
			t.Errorf("methodFor(%s) = %s, want %s", c.path, got, c.want)
		}
	}
	h := http.Header{}
	h.Add("Set-Cookie", "eb_session=old; Path=/; Max-Age=100")
	h.Add("Set-Cookie", "eb_session=; Path=/; Max-Age=0")
	h.Add("Set-Cookie", "eb_session=new; Path=/; Max-Age=100")
	h.Add("Set-Cookie", "other=x")
	if issuedToken(h) != "new" {
		t.Fatal("issuedToken")
	}
	if issuedToken(http.Header{}) != "" {
		t.Fatal("issuedToken empty")
	}
}

func TestOptionsHelpers(t *testing.T) {
	raw := "0123456789abcdef0123456789abcdef"
	if b, err := decodeSecret(raw); err != nil || string(b) != raw {
		t.Fatal("raw secret")
	}
	if b, err := decodeSecret(base64.StdEncoding.EncodeToString([]byte(raw))); err != nil || string(b) != raw {
		t.Fatal("base64 secret")
	}
	for _, bad := range []string{"short", base64.StdEncoding.EncodeToString([]byte("short"))} {
		if _, err := decodeSecret(bad); err == nil {
			t.Fatalf("decodeSecret(%q)", bad)
		}
	}
	o := Options{PublicURL: "https://mail.example.com/", CORSOrigins: []string{"https://admin.example.com/x", "https://mail.example.com", " "}}
	if got := o.trustedOrigins(); strings.Join(got, ",") != "https://mail.example.com,https://admin.example.com" {
		t.Fatalf("origins = %v", got)
	}
	if originOf("not a url") != "not a url" {
		t.Fatal("originOf fallback")
	}
	o.Env = "production"
	if ca := o.cookieAttrs(); !ca.secure || ca.domain != "" {
		t.Fatalf("prod cookie = %+v", ca)
	}
	o.Env, o.Auth.CookieDomain = "development", "mail.example.com"
	if ca := o.cookieAttrs(); ca.secure || ca.domain != "" {
		t.Fatalf("dev, own host = %+v", ca)
	}
	o.Auth.CookieDomain = ".example.com"
	if ca := o.cookieAttrs(); ca.domain != "example.com" {
		t.Fatalf("parent domain = %+v", ca)
	}
	d, idle, ua, short := o.sessionTimes()
	if d != 12*time.Hour || idle != 2*time.Hour || ua != 15*time.Minute || short != time.Hour {
		t.Fatalf("defaults = %v %v %v %v", d, idle, ua, short)
	}
	o.Auth.SessionTTL, o.Auth.SessionIdle = 40*time.Minute, 10*time.Minute
	d, idle, ua, short = o.sessionTimes()
	if d != 40*time.Minute || idle != 10*time.Minute || ua != 5*time.Minute || short != 20*time.Minute {
		t.Fatalf("small = %v %v %v %v", d, idle, ua, short)
	}
	if n, w := (Options{}).loginRule(); n != 10 || w != time.Minute {
		t.Fatal("login default")
	}
	if n, w := (Options{LoginRate: config.Rate{Count: 3, Period: time.Hour}}).loginRule(); n != 3 || w != time.Hour {
		t.Fatal("login rate")
	}
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "[2001:db8::1]:443"
	r.Header.Set("X-Forwarded-For", "6.6.6.6")
	if clientIPString(r) != "2001:db8::1" || rateLimitKey(r) != "ip:2001:db8::1" {
		t.Fatalf("ip = %s", clientIPString(r))
	}
	r.RemoteAddr = "192.0.2.1:1234"
	if clientIPString(r) != "192.0.2.1" {
		t.Fatal("ipv4")
	}
	if id, _ := (uuidGenerator{}).Generate(context.Background()); len(id.(string)) != 36 || (uuidGenerator{}).GetColumnType() != limen.ColumnTypeUUID {
		t.Fatal("uuid generator")
	}
	if _, err := New(context.Background(), Options{}, nil, nil); err == nil {
		t.Fatal("New without deps")
	}
}

func TestHTTPHelpers(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/v1/auth/signin/credential/", strings.NewReader(`{"credential":"a@b.c"}`))
	if m := peekJSON(r); stringField(m, "credential") != "a@b.c" {
		t.Fatal("peek")
	}
	if b, _ := io.ReadAll(r.Body); string(b) != `{"credential":"a@b.c"}` {
		t.Fatal("body not restored")
	}
	if route(r) != "/signin/credential" {
		t.Fatalf("route = %s", route(r))
	}
	if route(httptest.NewRequest(http.MethodGet, "/v1/auth", nil)) != "/" || route(httptest.NewRequest(http.MethodGet, "/v1/me", nil)) != "" {
		t.Fatal("route edge")
	}
	if peekJSON(httptest.NewRequest(http.MethodPost, "/", strings.NewReader("[1]"))) != nil {
		t.Fatal("non-object")
	}
	if peekJSON(httptest.NewRequest(http.MethodGet, "/", nil)) != nil {
		t.Fatal("no body")
	}
	big := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"a":"`+strings.Repeat("x", peekLimit)+`"}`))
	if peekJSON(big) != nil {
		t.Fatal("over the limit")
	}
	if b, _ := io.ReadAll(big.Body); len(b) != peekLimit+8 {
		t.Fatalf("big body restored = %d", len(b))
	}
	rec := httptest.NewRecorder()
	sr := &statusRecorder{ResponseWriter: rec}
	if sr.code() != 200 {
		t.Fatal("default status")
	}
	_, _ = sr.Write([]byte("x"))
	sr.WriteHeader(http.StatusInternalServerError)
	if sr.code() != 200 || sr.Unwrap() != rec {
		t.Fatal("status recorder")
	}
	retryAfter(rec, time.Now().Add(-time.Second))
	if rec.Header().Get("Retry-After") != "1" {
		t.Fatal("retry-after floor")
	}
	mw := &methodWriter{ResponseWriter: httptest.NewRecorder(), onHeader: func(int, http.Header) {}}
	_, _ = mw.Write([]byte("x"))
	if !mw.done || mw.Unwrap() == nil {
		t.Fatal("methodWriter")
	}
}

func TestLockoutUnit(t *testing.T) {
	kv := newFakeKV()
	s := &Service{o: Options{Auth: config.AuthConfig{LockoutThreshold: 2, LockoutDuration: time.Minute}}, kv: kv, log: discard()}
	status := 401
	h := s.lockout(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }))
	try := func(email string) int {
		r := httptest.NewRequest(http.MethodPost, "/v1/auth/signin/credential", strings.NewReader(`{"credential":"`+email+`"}`))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec.Code
	}
	if try("A@x.test") != 401 || try("a@x.test") != 401 {
		t.Fatal("failures")
	}
	if try("a@x.test") != 423 {
		t.Fatal("locked")
	}
	// other routes are untouched
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/auth/me", nil))
	if rec.Code != 401 {
		t.Fatal("passthrough")
	}
	// cache outages never block sign-in
	kv.fail = errors.New("down")
	status = 401
	if try("b@x.test") != 401 {
		t.Fatal("fail open on cache errors")
	}
	status = 200
	if try("b@x.test") != 200 {
		t.Fatal("success")
	}
	s.o.Auth.LockoutThreshold = 0
	if try("a@x.test") != 200 {
		t.Fatal("disabled")
	}
}

func TestMailerAndPasswordPolicy(t *testing.T) {
	if err := (NoopMailer{Log: discard()}).Enqueue(context.Background(), Mail{Kind: MailMFACode, To: "a@b.c"}); err != nil {
		t.Fatal(err)
	}
	if ValidatePassword(strings.Repeat("é", 12)) != nil || ValidatePassword("short") == nil || ValidatePassword(strings.Repeat("x", 129)) == nil {
		t.Fatal("password policy")
	}
	err := ValidatePassword("Unbelievable")
	var e *errs.E
	if !errors.As(err, &e) || e.Code != errs.CodeValidationFailed || e.Safe["reason"] != "common_password" {
		t.Fatalf("common password = %v", err)
	}
	if ValidatePassword("correct horse battery staple") != nil {
		t.Fatal("uncommon passphrase refused")
	}
	s := &Service{}
	h := s.passwordPolicy(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/v1/auth/passwords", strings.NewReader(`{"new_password":"`+strings.Repeat("x", 129)+`"}`)))
	if rec.Code != 422 {
		t.Fatalf("too long = %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/v1/auth/passwords", strings.NewReader(`{"new_password":"unbelievable"}`)))
	if rec.Code != 422 {
		t.Fatalf("common = %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPut, "/v1/auth/passwords", strings.NewReader(`{"new_password":"fine fine fine"}`)))
	if rec.Code != 204 {
		t.Fatal("ok password")
	}
}

func TestTOTPReplayUnit(t *testing.T) {
	kv := newFakeKV()
	s := &Service{kv: kv, log: discard(), secret: []byte("0123456789abcdef0123456789abcdef")}
	status := 200
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) })
	h := s.totpReplay(inner)
	uid := "user-1"
	ctx := WithIdentity(context.Background(), Identity{AuthUserID: uid})
	try := func(code string) int {
		r := httptest.NewRequest(http.MethodPost, "/v1/auth/two-factor/finalize-setup", strings.NewReader(`{"code":"`+code+`"}`))
		r = r.WithContext(ctx)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec.Code
	}
	if try("123456") != 200 {
		t.Fatal("first use")
	}
	if try("123456") != 401 {
		t.Fatal("reuse")
	}
	if try(" 123456 ") != 401 {
		t.Fatal("whitespace reuse")
	}
	status = 401
	if try("654321") != 401 {
		t.Fatal("limen reject")
	}
	status = 200
	if try("654321") != 200 {
		t.Fatal("released after reject")
	}
	kv.fail = errors.New("down")
	if try("111111") != 503 {
		t.Fatal("fail closed")
	}
	kv.fail = nil
	// non-string code is rejected (Limen would), not skipped
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/auth/two-factor/finalize-setup", strings.NewReader(`{"code":123456}`)).WithContext(ctx))
	if rec.Code != 422 {
		t.Fatalf("non-string code = %d", rec.Code)
	}
	// backup codes and padded method still classified correctly
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/auth/two-factor/verify", strings.NewReader(`{"code":"abcde-fghij"}`)))
	if rec.Code != 200 {
		t.Fatal("backup passthrough")
	}
	tryVerify := func(body string) int {
		r := httptest.NewRequest(http.MethodPost, "/v1/auth/two-factor/verify", strings.NewReader(body))
		plain, _ := json.Marshal(challenge{UserID: uid, Exp: time.Now().Add(time.Minute).Unix(), Type: challengeTokenType})
		tok, err := limen.EncryptXChaCha(string(plain), s.secret, nil)
		if err != nil {
			t.Fatal(err)
		}
		r.AddCookie(&http.Cookie{Name: ChallengeCookie, Value: tok})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec.Code
	}
	if tryVerify(`{"code":"999999","method":" totp"}`) != 200 {
		t.Fatal("padded method must be guarded as totp")
	}
	if tryVerify(`{"code":" 999999 ","method":"totp"}`) != 401 {
		t.Fatal("padded code must hit the same claim")
	}
}

func TestMFALockoutUnit(t *testing.T) {
	kv := newFakeKV()
	s := &Service{
		o:        Options{Auth: config.AuthConfig{LockoutThreshold: 2, LockoutDuration: time.Minute}},
		kv:       kv,
		log:      discard(),
		security: discard(),
		secret:   []byte("0123456789abcdef0123456789abcdef"),
	}
	// forge a valid session_2fa cookie
	plain, _ := json.Marshal(challenge{UserID: "u1", Exp: time.Now().Add(time.Minute).Unix(), Type: challengeTokenType})
	tok, err := limen.EncryptXChaCha(string(plain), s.secret, nil)
	if err != nil {
		t.Fatal(err)
	}
	status := http.StatusUnauthorized
	h := s.mfaLockout(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }))
	try := func() int {
		r := httptest.NewRequest(http.MethodPost, "/v1/auth/two-factor/verify", strings.NewReader(`{"code":"000000"}`))
		r.AddCookie(&http.Cookie{Name: ChallengeCookie, Value: tok})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec.Code
	}
	if c1, c2 := try(), try(); c1 != http.StatusUnauthorized || c2 != http.StatusUnauthorized {
		t.Fatalf("failures = %d %d", c1, c2)
	}
	if try() != http.StatusLocked {
		t.Fatal("locked")
	}
	s2 := &Service{
		o:        Options{Auth: config.AuthConfig{LockoutThreshold: 3, LockoutDuration: time.Minute}},
		kv:       newFakeKV(),
		log:      discard(),
		security: discard(),
		secret:   s.secret,
	}
	h2 := s2.mfaLockout(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))
	r := httptest.NewRequest(http.MethodPost, "/v1/auth/two-factor/verify", strings.NewReader(`{"code":"111111"}`))
	r.AddCookie(&http.Cookie{Name: ChallengeCookie, Value: tok})
	rec := httptest.NewRecorder()
	h2.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatal("success passthrough")
	}
}
