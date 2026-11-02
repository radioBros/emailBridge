package connect

import (
	"context"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/autoconfig"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

func TestModuleIdentity(t *testing.T) {
	m := &Module{}
	if m.Name() != "connect" || len(m.Roles()) == 0 {
		t.Fatal()
	}
	if err := m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if m.Service() != nil {
		t.Fatal()
	}
}

func TestPasswordErrors(t *testing.T) {
	st := newMemStore()
	token, _ := putLink(st, "")
	s := NewService(st, nil)
	s.Enabled = func(context.Context, string) bool { return true }
	boot, err := s.Bootstrap(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	_ = boot
	if _, err := s.PasswordConnect(context.Background(), token, PasswordInput{
		Email: "a@gmail.com", Password: "", CSRF: boot.CSRF,
	}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	if _, err := s.PasswordConnect(context.Background(), token, PasswordInput{
		Email: "a@gmail.com", Password: "x", CSRF: boot.CSRF,
	}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	s.Enabled = func(context.Context, string) bool { return false }
	if _, err := s.PasswordConnect(context.Background(), token, PasswordInput{
		Email: "a@gmail.com", Password: "x", CSRF: boot.CSRF,
		IMAP: &autoconfig.Server{Host: "h", Port: 993, TLS: "implicit"},
		SMTP: &autoconfig.Server{Host: "h", Port: 465, TLS: "implicit"},
	}); errs.CodeOf(err) != errs.CodeFeatureDisabled {
		t.Fatal(err)
	}
	s.Enabled = func(context.Context, string) bool { return true }
	s.Create = nil
	if _, err := s.PasswordConnect(context.Background(), token, PasswordInput{
		Email: "a@gmail.com", Password: "x", CSRF: boot.CSRF,
		IMAP: &autoconfig.Server{Host: "h", Port: 993, TLS: "implicit", User: "%EMAIL%"},
		SMTP: &autoconfig.Server{Host: "h", Port: 465, TLS: "implicit"},
	}); errs.CodeOf(err) != errs.CodeInternalError {
		t.Fatal(err)
	}
}

func TestOAuthStartErrors(t *testing.T) {
	st := newMemStore()
	token, _ := putLink(st, "")
	s := NewService(st, nil)
	s.Enabled = func(context.Context, string) bool { return true }
	boot, err := s.Bootstrap(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.OAuthStartPublic(context.Background(), token, OAuthStartInput{
		Email: "a@b.c", AppID: "bad", CSRF: boot.CSRF,
	}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	appID := store.NewID(store.PrefixOAuthApp)
	s.OAuthStart = nil
	if _, err := s.OAuthStartPublic(context.Background(), token, OAuthStartInput{
		Email: "a@b.c", AppID: appID, CSRF: boot.CSRF,
	}); errs.CodeOf(err) != errs.CodeInternalError {
		t.Fatal(err)
	}
}

func TestDiscoverNoDisco(t *testing.T) {
	st := newMemStore()
	token, _ := putLink(st, "")
	s := NewService(st, nil)
	s.Enabled = func(context.Context, string) bool { return true }
	boot, err := s.Bootstrap(context.Background(), token)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Discover(context.Background(), token, "a@b.c", boot.CSRF); errs.CodeOf(err) != errs.CodeAutoconfigFailed {
		t.Fatal(err)
	}
}

func TestLoadValidBadToken(t *testing.T) {
	s := NewService(newMemStore(), nil)
	if _, _, err := s.loadValid(context.Background(), "nope"); errs.CodeOf(err) != errs.CodeConnectLinkInvalid {
		t.Fatal(err)
	}
	id := store.NewID(store.PrefixConnectLink)
	if _, _, err := s.loadValid(context.Background(), id+".x"); errs.CodeOf(err) != errs.CodeConnectLinkInvalid {
		t.Fatal(err)
	}
	s2 := NewService(nil, nil)
	if _, _, err := s2.loadValid(context.Background(), id+".x"); errs.CodeOf(err) != errs.CodeConnectLinkInvalid {
		t.Fatal(err)
	}
}

func TestExpiredAndBadHash(t *testing.T) {
	st := newMemStore()
	token, link := putLink(st, "")
	link.ExpiresAt = time.Now().Add(-time.Hour)
	s := NewService(st, nil)
	if _, err := s.Bootstrap(context.Background(), token); errs.CodeOf(err) != errs.CodeConnectLinkInvalid {
		t.Fatal(err)
	}
	token2, link2 := putLink(st, "")
	link2.TokenHash = "deadbeef"
	if _, err := s.Bootstrap(context.Background(), token2); errs.CodeOf(err) != errs.CodeConnectLinkInvalid {
		t.Fatal(err)
	}
}

func TestCreateHostedValidation(t *testing.T) {
	st := newMemStore()
	s := NewService(st, nil)
	s.PublicURL = "https://gw.example.com"
	s.Enabled = func(context.Context, string) bool { return true }
	ctx := tenancy.WithCaller(context.Background(), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: "u", OrganizationID: "org_1", Role: tenancy.RoleAdmin,
	})
	if _, err := s.CreateHosted(ctx, HostedCreate{ReturnURL: "https://evil.test/"}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	if _, err := s.CreateHosted(ctx, HostedCreate{AppID: "oap_bad"}); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatal(err)
	}
	_, err := s.CreateHosted(ctx, HostedCreate{TTLSeconds: 1, AppID: store.NewID(store.PrefixOAuthApp)})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.CreateHosted(ctx, HostedCreate{TTLSeconds: int((8 * 24 * time.Hour) / time.Second)})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRepinErrors(t *testing.T) {
	s := NewService(nil, nil)
	if _, err := s.Repin(context.Background(), "acc_x", RepinInput{}); errs.CodeOf(err) != errs.CodeUnauthenticated {
		t.Fatal(err)
	}
	ctx := tenancy.WithCaller(context.Background(), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: "u", OrganizationID: "org_1", Role: tenancy.RoleAdmin,
	})
	if _, err := s.Repin(ctx, "acc_bad", RepinInput{}); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatal(err)
	}
	if _, err := s.Repin(ctx, store.NewID(store.PrefixAccount), RepinInput{}); errs.CodeOf(err) != errs.CodeInternalError {
		t.Fatal(err)
	}
}

func TestAPIHandlers(t *testing.T) {
	st := newMemStore()
	st.orgs["org_1"] = &orgDoc{ID: "org_1", Name: "A"}
	token, _ := putLink(st, "https://gw.example.com/")
	accID := store.NewID(store.PrefixAccount)
	st.accts[accID] = &accountDoc{ID: accID, OrganizationID: "org_1", Email: "a@b.c", Version: 1}
	s := NewService(st, autoconfig.NewService(nil))
	s.Enabled = func(context.Context, string) bool { return true }
	s.PublicURL = "https://gw.example.com"
	s.Create = func(context.Context, string, string, string, string, *autoconfig.Server, *autoconfig.Server) (string, error) {
		return accID, nil
	}
	s.OAuthStart = func(context.Context, string, string, string, string, string, string) (string, error) {
		return "https://idp/", nil
	}
	m := &Module{svc: s}
	ctx := tenancy.WithCaller(context.Background(), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: "u", OrganizationID: "org_1", Role: tenancy.RoleAdmin,
	})
	boot, err := m.bootstrap(context.Background(), &tokenIn{Token: token})
	if err != nil {
		t.Fatal(err)
	}
	imap := &autoconfig.Server{Host: "imap.gmail.com", Port: 993, TLS: "implicit", User: "%EMAIL%"}
	smtp := &autoconfig.Server{Host: "smtp.gmail.com", Port: 465, TLS: "implicit", User: "%EMAIL%"}
	if _, err := m.password(context.Background(), &passwordIn{Token: token, Body: PasswordInput{
		Email: "a@gmail.com", Password: "p", CSRF: boot.Body.CSRF, IMAP: imap, SMTP: smtp, Name: "N",
	}}); err != nil {
		t.Fatal(err)
	}
	token2, _ := putLink(st, "")
	boot2, _ := m.bootstrap(context.Background(), &tokenIn{Token: token2})
	appID := store.NewID(store.PrefixOAuthApp)
	if _, err := m.oauthStart(context.Background(), &oauthStartIn{Token: token2, Body: OAuthStartInput{
		Email: "a@gmail.com", AppID: appID, CSRF: boot2.Body.CSRF,
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.repin(ctx, &repinIn{AccountID: accID, Body: RepinInput{IMAPUser: "a@b.c"}}); err != nil {
		t.Fatal(err)
	}
}

func TestHostedOnDefaultRegistry(t *testing.T) {
	s := NewService(nil, nil)
	s.Enabled = nil
	s.Registry = features.DefaultRegistry()
	s.Env = "production"
	if s.hostedOn(context.Background(), "o") {
		t.Fatal("hosted default off")
	}
}

func TestMigration(t *testing.T) {
	m := Migration(36)
	if m.Number != 36 || m.Name == "" || m.Up == nil {
		t.Fatal(m)
	}
}

// CSRF tokens are stateless: a token from one API node is accepted by another with the same key, refused
// with another key, for another link, after expiry, or tampered.
func TestCSRFStatelessAcrossNodes(t *testing.T) {
	key := []byte("shared-key-0123456789abcdef01234")
	now := time.Unix(1_800_000_000, 0)
	a, b := NewService(nil, nil), NewService(nil, nil)
	a.CSRFKey, b.CSRFKey = key, key
	a.clock = func() time.Time { return now }
	b.clock = a.clock

	tok := a.issueCSRF("hash-1")
	if err := b.checkCSRF("hash-1", tok); err != nil {
		t.Fatalf("other node: %v", err)
	}
	if err := b.checkCSRF("hash-2", tok); err == nil {
		t.Fatal("token is bound to its link")
	}
	c := NewService(nil, nil)
	c.clock = a.clock
	if err := c.checkCSRF("hash-1", tok); err == nil {
		t.Fatal("another key must refuse")
	}
	for _, bad := range []string{"", "nodot", "x." + tok, tok + "x", "99999999999.abc"} {
		if err := b.checkCSRF("hash-1", bad); err == nil {
			t.Fatalf("tampered %q accepted", bad)
		}
	}
	b.clock = func() time.Time { return now.Add(csrfTTL + time.Second) }
	if err := b.checkCSRF("hash-1", tok); err == nil {
		t.Fatal("expired token accepted")
	}
}
