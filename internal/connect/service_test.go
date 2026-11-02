package connect

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/autoconfig"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

type memStore struct {
	mu    sync.Mutex
	links map[string]*Link
	orgs  map[string]*orgDoc
	accts map[string]*accountDoc
	apps  []OAuthAppBrief
}

func newMemStore() *memStore {
	return &memStore{
		links: map[string]*Link{},
		orgs:  map[string]*orgDoc{},
		accts: map[string]*accountDoc{},
	}
}

func (m *memStore) InsertLink(_ context.Context, l *Link) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *l
	m.links[l.ID] = &cp
	return nil
}

func (m *memStore) GetLink(_ context.Context, id string) (*Link, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.links[id]
	if !ok {
		return nil, errs.New(errs.CodeConnectLinkInvalid, "missing")
	}
	cp := *l
	return &cp, nil
}

func (m *memStore) MarkLinkUsed(_ context.Context, id string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.links[id]
	if !ok || l.UsedAt != nil {
		return errs.New(errs.CodeConnectLinkInvalid, "used")
	}
	l.UsedAt = &at
	return nil
}

func (m *memStore) GetOrg(_ context.Context, id string) (*orgDoc, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.orgs[id]
	if !ok {
		return nil, errs.New(errs.CodeNotFound, "org")
	}
	cp := *o
	if o.Branding != nil {
		cp.Branding = map[string]any{}
		for k, v := range o.Branding {
			cp.Branding[k] = v
		}
	}
	return &cp, nil
}

func (m *memStore) SetOrgBranding(_ context.Context, id string, branding map[string]any, version int64) (*orgDoc, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	o, ok := m.orgs[id]
	if !ok || o.Version != version {
		return nil, errs.New(errs.CodeConflict, "conflict")
	}
	o.Branding = branding
	o.Version++
	cp := *o
	return &cp, nil
}

func (m *memStore) ListOAuthApps(context.Context, string) ([]OAuthAppBrief, error) {
	return m.apps, nil
}

func (m *memStore) GetAccount(_ context.Context, org, id string) (*accountDoc, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.accts[id]
	if !ok || a.OrganizationID != org {
		return nil, errs.New(errs.CodeNotFound, "account")
	}
	cp := *a
	return &cp, nil
}

func (m *memStore) SetIdentity(_ context.Context, org, id string, pin Identity, version int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.accts[id]
	if !ok || a.OrganizationID != org || a.Version != version {
		return errs.New(errs.CodeConflict, "conflict")
	}
	a.Identity = &pin
	a.Version++
	return nil
}

func putLink(m *memStore, returnURL string) (token string, link *Link) {
	id := store.NewID(store.PrefixConnectLink)
	secret := "sekritvalue"
	sum := sha256.Sum256([]byte(id + "." + secret))
	link = &Link{
		ID: id, OrganizationID: "org_1", Mode: ModeHosted, ReturnURL: returnURL,
		TokenHash: hex.EncodeToString(sum[:]), ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now(),
	}
	m.links[id] = link
	return id + "." + secret, link
}

func TestValidateReturnURL(t *testing.T) {
	if err := validateReturnURL("https://gw.example.com", "https://gw.example.com/done", nil, true); err != nil {
		t.Fatal(err)
	}
	if err := validateReturnURL("https://gw.example.com", "http://localhost:3000/x", nil, false); err != nil {
		t.Fatal(err)
	}
	if err := validateReturnURL("https://gw.example.com", "http://evil.test/x", nil, true); err == nil {
		t.Fatal("expected reject")
	}
	if err := validateReturnURL("https://gw.example.com", "https://evil.test/x", nil, true); err == nil {
		t.Fatal("expected host reject")
	}
}

func TestSplitAndExpand(t *testing.T) {
	id := store.NewID(store.PrefixConnectLink)
	gotID, sec, ok := splitConnectToken(id + ".secret")
	if !ok || gotID != id || sec != "secret" {
		t.Fatal(gotID, sec, ok)
	}
	if _, _, ok := splitConnectToken("bad"); ok {
		t.Fatal()
	}
	if expandUser("%EMAIL%", "a@b.c") != "a@b.c" || expandUser("%EMAILLOCALPART%", "a@b.c") != "a" {
		t.Fatal()
	}
	if _, err := normalizeEmail("Ada <A@B.C>"); err != nil {
		t.Fatal(err)
	}
	if _, err := normalizeEmail("nope"); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
}

func TestCSRFAndRate(t *testing.T) {
	s := NewService(nil, nil)
	s.clock = func() time.Time { return time.Unix(100, 0) }
	tok := s.issueCSRF("hash1")
	if err := s.checkCSRF("hash1", tok); err != nil {
		t.Fatal(err)
	}
	if err := s.checkCSRF("hash1", "nope"); errs.CodeOf(err) != errs.CodeConnectCSRFInvalid {
		t.Fatal(err)
	}
	s.clock = func() time.Time { return time.Unix(100, 0).Add(csrfTTL + time.Second) }
	if err := s.checkCSRF("hash1", tok); errs.CodeOf(err) != errs.CodeConnectCSRFInvalid {
		t.Fatal(err)
	}
	s.clock = func() time.Time { return time.Unix(0, 0) }
	for i := 0; i < 10; i++ {
		if err := s.takePublicRate("k", 10); err != nil {
			t.Fatal(i, err)
		}
	}
	if err := s.takePublicRate("k", 10); errs.CodeOf(err) != errs.CodeRateLimited {
		t.Fatal(err)
	}
}

func TestCreateHosted(t *testing.T) {
	st := newMemStore()
	s := NewService(st, nil)
	s.PublicURL = "https://gw.example.com"
	s.Enabled = func(context.Context, string) bool { return true }
	ctx := tenancy.WithCaller(context.Background(), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: "u", OrganizationID: "org_1", Role: tenancy.RoleAdmin,
	})
	out, err := s.CreateHosted(ctx, HostedCreate{ReturnURL: "https://gw.example.com/ok", TTLSeconds: 120})
	if err != nil || !strings.Contains(out.URL, "/connect/") || len(st.links) != 1 {
		t.Fatalf("%+v %v", out, err)
	}
	s.Enabled = func(context.Context, string) bool { return false }
	if _, err := s.CreateHosted(ctx, HostedCreate{}); errs.CodeOf(err) != errs.CodeFeatureDisabled {
		t.Fatal(err)
	}
	if _, err := s.CreateHosted(context.Background(), HostedCreate{}); errs.CodeOf(err) != errs.CodeUnauthenticated {
		t.Fatal(err)
	}
}

func TestPublicFlow(t *testing.T) {
	st := newMemStore()
	st.orgs["org_1"] = &orgDoc{ID: "org_1", Name: "Acme", Version: 1, Branding: map[string]any{
		"brandName": "Acme Mail", "primaryColor": "#336699", "logoRef": "https://cdn/logo.png",
		"headerHtml": `<p>Hello</p><script>alert(1)</script>`,
		"headHtml":   `<meta name="description" content="x"><script>x</script>`,
	}}
	appID := store.NewID(store.PrefixOAuthApp)
	st.apps = []OAuthAppBrief{{ID: appID, Name: "Gmail", Provider: "gmail"}}
	token, link := putLink(st, "https://gw.example.com/done")
	st.accts["acc_01"] = &accountDoc{ID: "acc_01", OrganizationID: "org_1", Email: "user@gmail.com", Version: 1}

	disco := autoconfig.NewService(nil)
	s := NewService(st, disco)
	s.PublicURL = "https://gw.example.com"
	s.Enabled = func(context.Context, string) bool { return true }
	s.Create = func(_ context.Context, org, name, email, pass string, imap, smtp *autoconfig.Server) (string, error) {
		if org != "org_1" || pass == "" || imap == nil || smtp == nil {
			t.Fatal("create args")
		}
		return "acc_01", nil
	}
	s.OAuthStart = func(context.Context, string, string, string, string, string, string) (string, error) {
		return "https://idp.example/auth", nil
	}

	boot, err := s.Bootstrap(context.Background(), token)
	if err != nil || boot.Branding.BrandName != "Acme Mail" || boot.CSRF == "" || len(boot.OAuthApps) != 1 {
		t.Fatalf("%+v %v", boot, err)
	}
	if !strings.Contains(boot.Branding.HeaderHTML, "Hello") || strings.Contains(strings.ToLower(boot.Branding.HeaderHTML), "<script") {
		t.Fatalf("headerHtml not sanitised: %q", boot.Branding.HeaderHTML)
	}
	if strings.Contains(strings.ToLower(boot.Branding.HeadHTML), "<script") {
		t.Fatalf("headHtml not sanitised: %q", boot.Branding.HeadHTML)
	}
	res, err := s.Discover(context.Background(), token, "user@gmail.com", boot.CSRF)
	if err != nil || res.Source != autoconfig.SourceBuiltin {
		t.Fatalf("%+v %v", res, err)
	}
	done, err := s.PasswordConnect(context.Background(), token, PasswordInput{
		Email: "user@gmail.com", Password: "pw", CSRF: boot.CSRF, IMAP: res.IMAP, SMTP: res.SMTP,
	})
	if err != nil || done.AccountID != "acc_01" || done.RedirectURL != link.ReturnURL {
		t.Fatalf("%+v %v", done, err)
	}
	if st.accts["acc_01"].Identity == nil || st.accts["acc_01"].Identity.Email != "user@gmail.com" {
		t.Fatalf("pin %+v", st.accts["acc_01"].Identity)
	}

	// link used — bootstrap fails
	if _, err := s.Bootstrap(context.Background(), token); errs.CodeOf(err) != errs.CodeConnectLinkInvalid {
		t.Fatal(err)
	}

	token2, _ := putLink(st, "")
	boot2, _ := s.Bootstrap(context.Background(), token2)
	oauth, err := s.OAuthStartPublic(context.Background(), token2, OAuthStartInput{
		Email: "user@gmail.com", AppID: appID, CSRF: boot2.CSRF,
	})
	if err != nil || oauth.AuthorizeURL == "" {
		t.Fatalf("%+v %v", oauth, err)
	}
	if _, err := s.Discover(context.Background(), token2, "user@gmail.com", "bad"); errs.CodeOf(err) != errs.CodeConnectCSRFInvalid {
		t.Fatal(err)
	}
}

func TestRepin(t *testing.T) {
	st := newMemStore()
	accID := store.NewID(store.PrefixAccount)
	st.accts[accID] = &accountDoc{ID: accID, OrganizationID: "org_1", Email: "a@b.test", Version: 1}
	s := NewService(st, nil)
	ctx := tenancy.WithCaller(context.Background(), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: "u", OrganizationID: "org_1", Role: tenancy.RoleAdmin,
	})
	pin, err := s.Repin(ctx, accID, RepinInput{IMAPUser: "a@b.test"})
	if err != nil || pin.Email != "a@b.test" || st.accts[accID].Identity == nil {
		t.Fatalf("%+v %v", pin, err)
	}
}

func TestModuleHandlers(t *testing.T) {
	st := newMemStore()
	st.orgs["org_1"] = &orgDoc{ID: "org_1", Name: "Acme"}
	token, _ := putLink(st, "https://gw.example.com/")
	s := NewService(st, autoconfig.NewService(nil))
	s.Enabled = func(context.Context, string) bool { return true }
	s.PublicURL = "https://gw.example.com"
	s.Create = func(context.Context, string, string, string, string, *autoconfig.Server, *autoconfig.Server) (string, error) {
		return "acc_x", nil
	}
	m := &Module{svc: s}
	ctx := tenancy.WithCaller(context.Background(), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: "u", OrganizationID: "org_1", Role: tenancy.RoleAdmin,
	})
	created, err := m.createHosted(ctx, &createHostedIn{Body: HostedCreate{}})
	if err != nil || created.Body.ID == "" {
		t.Fatal(err)
	}
	boot, err := m.bootstrap(context.Background(), &tokenIn{Token: token})
	if err != nil {
		t.Fatal(err)
	}
	_, err = m.discover(context.Background(), &discoverIn{Token: token, Body: struct {
		Email string `json:"email"`
		CSRF  string `json:"csrf"`
	}{Email: "a@gmail.com", CSRF: boot.Body.CSRF}})
	if err != nil {
		t.Fatal(err)
	}
}

func TestUpdateBranding(t *testing.T) {
	st := newMemStore()
	st.orgs["org_1"] = &orgDoc{ID: "org_1", Name: "Acme", Version: 1}
	s := NewService(st, nil)
	ctx := tenancy.WithCaller(context.Background(), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: "u", OrganizationID: "org_1", Role: tenancy.RoleAdmin,
	})
	header := `<h2>Hi</h2><img src=x onerror=alert(1)>`
	head := `<style>p{color:red}</style><script>bad</script>`
	b, ver, err := s.UpdateBranding(ctx, BrandingUpdate{HeaderHTML: &header, HeadHTML: &head}, "")
	if err != nil || ver != 2 || strings.Contains(b.HeaderHTML, "onerror") || strings.Contains(b.HeadHTML, "script") {
		t.Fatalf("%+v %d %v", b, ver, err)
	}
	got, _, err := s.GetBranding(ctx)
	if err != nil || got.HeaderHTML != b.HeaderHTML {
		t.Fatal(err)
	}
	prev, err := s.PreviewBrandingHTML(ctx, BrandingPreviewRequest{HeaderHTML: header})
	if err != nil || prev.HeaderHTML != b.HeaderHTML {
		t.Fatalf("%+v %v", prev, err)
	}
}

func TestBrandingFromAndHostedOn(t *testing.T) {
	s := NewService(nil, nil)
	b := s.brandingFrom(context.Background(), &orgDoc{Name: "N", Branding: map[string]any{"brandName": "B", "logoRef": "https://cdn.example/logo.png"}})
	if b.BrandName != "B" || b.LogoURL != "https://cdn.example/logo.png" {
		t.Fatal(b)
	}
	s.Enabled = nil
	if s.hostedOn(context.Background(), "org") {
		t.Fatal("default off")
	}
}

func TestSubtleEqual(t *testing.T) {
	if !subtleEqual("ab", "ab") || subtleEqual("ab", "ac") || subtleEqual("a", "ab") {
		t.Fatal()
	}
	if _, err := randomURLString(8); err != nil {
		t.Fatal(err)
	}
}
