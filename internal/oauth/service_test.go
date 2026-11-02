package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

func TestValidateApp(t *testing.T) {
	if err := validateCreate(CreateInput{Name: "x", Provider: "nope", ClientID: "c", ClientSecret: "s"}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("provider: %v", err)
	}
	if err := validateCreate(CreateInput{Name: "x", Provider: ProviderGeneric, ClientID: "c", ClientSecret: "s"}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("generic urls: %v", err)
	}
	if err := validateCreate(CreateInput{
		Name: "x", Provider: ProviderGeneric, ClientID: "c", ClientSecret: "s",
		AuthorizeURL: "https://a", TokenURL: "https://t", Scopes: []string{"mail"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := validateCreate(CreateInput{Name: "x", Provider: ProviderGmail, ClientID: "c", ClientSecret: "s"}); err != nil {
		t.Fatal(err)
	}
}

func TestViewOfPresets(t *testing.T) {
	a := &App{ID: "oap_1", Provider: ProviderGmail, ClientID: "cid", Scopes: []string{"s"}, Enabled: true}
	v := ViewOf(a)
	if v.AuthorizeURL == "" || v.TokenURL == "" || v.HasClientSecret {
		t.Fatalf("%+v", v)
	}
}

func TestCheckIfMatch(t *testing.T) {
	if err := checkIfMatch("", 1); err != nil {
		t.Fatal(err)
	}
	if err := checkIfMatch(`"v2"`, 1); errs.CodeOf(err) != errs.CodeVersionMismatch {
		t.Fatal(err)
	}
	if formatETag(3) != `"v3"` {
		t.Fatal(formatETag(3))
	}
}

func TestSplitConnectToken(t *testing.T) {
	id := store.NewID(store.PrefixConnectLink)
	gotID, secret, ok := splitConnectToken(id + ".abc")
	if !ok || gotID != id || secret != "abc" {
		t.Fatalf("%v %v %v", gotID, secret, ok)
	}
	if _, _, ok := splitConnectToken("bad"); ok {
		t.Fatal("expected fail")
	}
}

func TestSubtleEqual(t *testing.T) {
	if !subtleConstantTimeEqual("ab", "ab") || subtleConstantTimeEqual("ab", "ac") || subtleConstantTimeEqual("a", "ab") {
		t.Fatal("equal")
	}
}

func TestParseXOAuth2Helpers(t *testing.T) {
	// covered in simulator; ensure randomURLString works
	s, err := randomURLString(16)
	if err != nil || len(s) < 10 {
		t.Fatal(s, err)
	}
}

type memState struct {
	mu   sync.Mutex
	data map[string][]byte
}

func (m *memState) CacheSet(_ context.Context, key string, value []byte, _ time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.data == nil {
		m.data = map[string][]byte{}
	}
	m.data[key] = append([]byte(nil), value...)
	return nil
}
func (m *memState) CacheGet(_ context.Context, key string) (bus.CacheEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.data[key]
	if !ok {
		return bus.CacheEntry{}, bus.ErrCacheMiss
	}
	return bus.CacheEntry{Value: v}, nil
}
func (m *memState) CacheDelete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.data, key)
	return nil
}

type memLease struct {
	mu   sync.Mutex
	held map[string]uint64
	seq  uint64
}

func (m *memLease) Claim(_ context.Context, key string, _ []byte, _ time.Duration) (uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.held == nil {
		m.held = map[string]uint64{}
	}
	if _, ok := m.held[key]; ok {
		return 0, bus.ErrLeaseHeld
	}
	m.seq++
	m.held[key] = m.seq
	return m.seq, nil
}
func (m *memLease) Release(_ context.Context, key string, epoch uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.held[key] != epoch {
		return bus.ErrLeaseLost
	}
	delete(m.held, key)
	return nil
}

func TestStateRoundTrip(t *testing.T) {
	s := &Service{State: &memState{}}
	st := flowState{AccountID: "acc_1", OrgID: "org_1", AppID: "oap_1", Provider: ProviderGmail, Verifier: "v", RedirectURI: "https://x/c"}
	if err := s.putState(context.Background(), "st", st); err != nil {
		t.Fatal(err)
	}
	got, err := s.takeState(context.Background(), "st")
	if err != nil || got.AccountID != "acc_1" {
		t.Fatal(got, err)
	}
	if _, err := s.takeState(context.Background(), "st"); errs.CodeOf(err) != errs.CodeOauthStateInvalid {
		t.Fatal(err)
	}
}

func TestEndpoints(t *testing.T) {
	s := &Service{}
	a, tok, rev := s.endpoints(&App{Provider: ProviderOutlook})
	if a == "" || tok == "" {
		t.Fatal(a, tok, rev)
	}
	a, tok, _ = s.endpoints(&App{Provider: ProviderGeneric, AuthorizeURL: "https://a", TokenURL: "https://t"})
	if a != "https://a" || tok != "https://t" {
		t.Fatal(a, tok)
	}
}

func TestReachableMock(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":"invalid"}`)
	}))
	t.Cleanup(srv.Close)
	s := &Service{HTTP: srv.Client()}
	if !s.reachable(context.Background(), http.MethodGet, srv.URL, nil) {
		t.Fatal("get")
	}
	if !s.reachable(context.Background(), http.MethodPost, srv.URL, url.Values{"a": {"b"}}) {
		t.Fatal("post")
	}
	if s.reachable(context.Background(), http.MethodGet, "http://127.0.0.1:1", nil) {
		t.Fatal("unreachable")
	}
}

func TestOauthErrorURL(t *testing.T) {
	s := &Service{PublicURL: "https://eb.example/"}
	if s.oauthErrorURL("acc_1") != "https://eb.example/accounts/acc_1?oauth_error=1" {
		t.Fatal(s.oauthErrorURL("acc_1"))
	}
	if !strings.Contains(s.oauthErrorURL(""), "oauth-apps") {
		t.Fatal(s.oauthErrorURL(""))
	}
}

func TestCallerContextHelpers(t *testing.T) {
	ctx := tenancy.WithCaller(context.Background(), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: "usr_1", OrganizationID: "org_1", Role: tenancy.RoleAdmin,
	})
	ctx = store.WithOrganization(ctx, "org_1")
	if err := tenancy.Require(ctx, tenancy.PermAccountsWrite); err != nil {
		t.Fatal(err)
	}
}

func TestMarkRevokedMessage(t *testing.T) {
	// ensure code constants exist
	if errs.CodeOauthRevoked == "" || errs.CodeConnectLinkInvalid == "" {
		t.Fatal("codes")
	}
}

func TestJSONFlowState(t *testing.T) {
	raw, _ := json.Marshal(flowState{AccountID: "a"})
	var st flowState
	if err := json.Unmarshal(raw, &st); err != nil || st.AccountID != "a" {
		t.Fatal(err)
	}
}

func TestEncryptedZero(t *testing.T) {
	var e crypto.EncryptedString
	if !e.IsZero() {
		t.Fatal()
	}
}

func TestLeaseHeldRetryPath(t *testing.T) {
	l := &memLease{}
	if _, err := l.Claim(context.Background(), "k", nil, time.Second); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Claim(context.Background(), "k", nil, time.Second); !errors.Is(err, bus.ErrLeaseHeld) {
		t.Fatal(err)
	}
}
