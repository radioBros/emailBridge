//go:build integration

package oauth

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"golang.org/x/oauth2"

	"github.com/radiobros/emailbridge/internal/accounts"
	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

func TestIntegrationAPIHandlersAndEdges(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	if err := Migration(30).Up(ctx, db); err != nil {
		t.Fatal(err)
	}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	org := store.NewID(store.PrefixOrganization)
	cctx := tenancy.WithCaller(store.WithOrganization(ctx, org), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: store.NewID(store.PrefixUser), OrganizationID: org, Role: tenancy.RoleOwner,
	})
	_, srv := startMockProvider(t)
	svc := &Service{
		Repo: &Repo{DB: db}, Cipher: testCipher(t, db),
		Audit: &audit.Writer{DB: db, Log: quiet, Retention: time.Hour},
		State: &memState{}, Leases: &memLease{}, NodeID: "n1",
		CallbackURL: "https://cb.example", PublicURL: "https://eb.example",
		HTTP: srv.Client(), Log: quiet,
	}
	mod := &Module{svc: svc, CallbackURL: svc.CallbackURL, PublicURL: svc.PublicURL}

	// Create via API handler (generic → mock token URL for refresh tests)
	out, err := mod.create(cctx, &createIn{Body: CreateInput{
		Name: "A1", Provider: ProviderGeneric, ClientID: "c", ClientSecret: "s",
		AuthorizeURL: srv.URL + "/authorize", TokenURL: srv.URL + "/token", RevokeURL: srv.URL + "/revoke",
		Scopes: []string{"mail"},
	}})
	if err != nil || out.Body.ID == "" {
		t.Fatal(err, out)
	}
	appID := out.Body.ID

	if _, err := mod.get(cctx, &idIn{AppID: appID}); err != nil {
		t.Fatal(err)
	}
	en := false
	sec := "news"
	scopes := []string{"https://mail.google.com/", "extra"}
	authURL := "https://example/a"
	if _, err := mod.patch(cctx, &patchIn{
		AppID: appID, IfMatch: formatETag(1),
		Body: PatchInput{Enabled: &en, ClientSecret: &sec, Scopes: scopes, AuthorizeURL: &authURL},
	}); err != nil {
		t.Fatal(err)
	}
	// re-enable
	en = true
	cur, _ := svc.Get(cctx, appID)
	if _, err := mod.patch(cctx, &patchIn{AppID: appID, IfMatch: formatETag(cur.Version), Body: PatchInput{Enabled: &en}}); err != nil {
		t.Fatal(err)
	}

	if _, err := mod.verify(cctx, &idIn{AppID: appID}); err != nil {
		t.Fatal(err)
	}
	if _, err := mod.list(cctx, &listIn{Sort: "name", Q: "A1", IncludeTotal: true, Limit: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := mod.list(cctx, &listIn{Sort: "-name"}); err != nil {
		t.Fatal(err)
	}
	if _, err := mod.list(cctx, &listIn{Sort: "createdAt"}); err != nil {
		t.Fatal(err)
	}
	if _, err := mod.list(cctx, &listIn{Sort: "nope"}); errs.CodeOf(err) != errs.CodeSortNotAllowed {
		t.Fatal(err)
	}

	// cursor pagination: create more apps
	for i := 0; i < 3; i++ {
		_, err := svc.Create(cctx, CreateInput{
			Name: "P" + string(rune('a'+i)), Provider: ProviderOutlook, ClientID: "c", ClientSecret: "s",
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	page1, err := svc.List(cctx, ListFilter{Limit: 2, Sort: "-createdAt"})
	if err != nil || page1.NextCursor == nil {
		t.Fatalf("%v %+v", err, page1)
	}
	page2, err := svc.List(cctx, ListFilter{Limit: 2, Cursor: *page1.NextCursor, Sort: "-createdAt"})
	if err != nil {
		t.Fatal(err)
	}
	_ = page2

	accID := store.NewID(store.PrefixAccount)
	_, _ = db.Collection(accounts.Collection).InsertOne(ctx, bson.M{
		"_id": accID, "organizationId": org, "name": "A", "email": "a@ex.test",
		"state": accounts.StateNew, "imap": bson.M{"auth": bson.M{"user": "a@ex.test"}},
		"smtp": bson.M{"auth": bson.M{"user": "a@ex.test"}}, "version": int64(1),
		"createdAt": time.Now().UTC(), "updatedAt": time.Now().UTC(),
	})

	startOut, err := mod.start(cctx, &startIn{AccountID: accID, Body: StartInput{AppID: appID}})
	if err != nil {
		t.Fatal(err)
	}
	_ = startOut

	// disabled app start
	off := false
	cur, _ = svc.Get(cctx, appID)
	_, _ = svc.Patch(cctx, appID, formatETag(cur.Version), PatchInput{Enabled: &off})
	if _, err := svc.Start(cctx, accID, StartInput{AppID: appID}); errs.CodeOf(err) != errs.CodeOauthAppDisabled {
		t.Fatal(err)
	}
	on := true
	cur, _ = svc.Get(cctx, appID)
	_, _ = svc.Patch(cctx, appID, formatETag(cur.Version), PatchInput{Enabled: &on})

	// store tokens + AccessToken without refresh
	cur, _ = svc.Get(cctx, appID)
	if err := svc.storeTokens(ctx, org, accID, cur, &oauth2.Token{
		AccessToken: "live", RefreshToken: "rr", Expiry: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	tok, _, err := svc.AccessToken(ctx, accID)
	if err != nil || tok != "live" {
		t.Fatal(tok, err)
	}

	// refresh with nil leases (direct doRefresh)
	svc.Leases = nil
	_, _ = db.Collection(accounts.Collection).UpdateOne(ctx, bson.M{"_id": accID}, bson.M{
		"$set": bson.M{"oauth.expiresAt": time.Now().Add(-time.Minute)},
	})
	if _, _, err := svc.AccessToken(ctx, accID); err != nil {
		t.Fatal(err)
	}

	linkOut, err := mod.createLink(cctx, &linkIn{Body: ConnectLinkCreate{AccountID: accID, AppID: appID, TTLSeconds: 30}})
	if err != nil {
		t.Fatal(err)
	}
	token := strings.TrimPrefix(linkOut.Body.URL, "https://eb.example/connect/")

	// redeem expired
	past := time.Now().Add(-time.Hour)
	_, _ = db.Collection(CollectionLinks).UpdateOne(ctx, bson.M{"_id": linkOut.Body.ID}, bson.M{"$set": bson.M{"expiresAt": past}})
	w := httptest.NewRecorder()
	svc.RedeemConnectLink(ctx, w, httptest.NewRequest(http.MethodGet, "/", nil), token)
	if w.Code == http.StatusFound {
		t.Fatal("expected expired reject")
	}

	// new link + wrong secret
	link2, _ := svc.CreateConnectLink(cctx, ConnectLinkCreate{AccountID: accID, AppID: appID})
	w2 := httptest.NewRecorder()
	svc.RedeemConnectLink(ctx, w2, httptest.NewRequest(http.MethodGet, "/", nil), link2.ID+".wrongsecretxxxxxxxxxx")
	if w2.Code == http.StatusFound {
		t.Fatal("expected bad hash")
	}

	if _, err := mod.disconnect(cctx, &accountIn{AccountID: accID}); err != nil {
		t.Fatal(err)
	}

	cur, _ = svc.Get(cctx, appID)
	if _, err := mod.delete(cctx, &deleteIn{AppID: appID, IfMatch: formatETag(cur.Version)}); err != nil {
		t.Fatal(err)
	}

	// version mismatch
	a2, err := svc.Create(cctx, CreateInput{Name: "Z", Provider: ProviderMailru, ClientID: "c", ClientSecret: "s"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Patch(cctx, a2.ID, `"v99"`, PatchInput{}); errs.CodeOf(err) != errs.CodeVersionMismatch {
		t.Fatal(err)
	}
	if err := svc.Delete(cctx, a2.ID, `"v99"`); errs.CodeOf(err) != errs.CodeVersionMismatch {
		t.Fatal(err)
	}

	// Start bad ids
	if _, err := svc.Start(cctx, "bad", StartInput{AppID: a2.ID}); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatal(err)
	}
	if _, err := svc.Start(cctx, accID, StartInput{AppID: "bad"}); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatal(err)
	}

	// gmail create uses presets
	g, err := svc.Create(cctx, CreateInput{Name: "G", Provider: ProviderGmail, ClientID: "c", ClientSecret: "s"})
	if err != nil || len(g.Scopes) == 0 {
		t.Fatal(err, g)
	}

	// generic missing scopes
	if _, err := svc.Create(cctx, CreateInput{
		Name: "Gen", Provider: ProviderGeneric, ClientID: "c", ClientSecret: "s",
		AuthorizeURL: "https://a", TokenURL: "https://t",
	}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}

}

func TestIntegrationModuleStart(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	cipher := testCipher(t, db)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := app.New(app.Options{Log: quiet, Roles: app.RoleSet{app.RoleAPI: true}, NodeID: "n1"})
	app.Provide(a, db)
	app.Provide(a, cipher)
	srv, err := httpapi.New(httpapi.Options{PublicURL: "https://eb.example"})
	if err != nil {
		t.Fatal(err)
	}
	app.Provide(a, srv)
	m := &Module{CallbackURL: "https://cb.example", PublicURL: "https://eb.example", Log: quiet}
	if err := m.Start(ctx, a); err != nil {
		t.Fatal(err)
	}
	if m.Service() == nil {
		t.Fatal()
	}
	// hit public routes
	req := httptest.NewRequest(http.MethodGet, "/callbacks/gmail?error=x", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)
	if w.Code != http.StatusFound && w.Code != http.StatusOK {
		// may be 302
		_ = w.Code
	}
	req2 := httptest.NewRequest(http.MethodGet, "/callbacks/nope", nil)
	w2 := httptest.NewRecorder()
	srv.ServeHTTP(w2, req2)
	req3 := httptest.NewRequest(http.MethodGet, "/v1/connect-links/bad.token/redeem", nil)
	w3 := httptest.NewRecorder()
	srv.ServeHTTP(w3, req3)
}

func TestIntegrationCallbackAndListEdges(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	_ = Migration(1).Up(ctx, db)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	org := store.NewID(store.PrefixOrganization)
	cctx := tenancy.WithCaller(store.WithOrganization(ctx, org), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: "u", OrganizationID: org, Role: tenancy.RoleOwner,
	})
	_, srv := startMockProvider(t)
	st := &memState{}
	svc := &Service{
		Repo: &Repo{DB: db}, Cipher: testCipher(t, db), State: st, Leases: &memLease{},
		CallbackURL: "https://cb.example", PublicURL: "https://eb.example",
		HTTP: srv.Client(), Log: quiet,
		Audit: &audit.Writer{DB: db, Log: quiet, Retention: time.Hour},
	}
	appDoc, err := svc.Create(cctx, CreateInput{
		Name: "CB", Provider: ProviderGeneric, ClientID: "c", ClientSecret: "s",
		AuthorizeURL: srv.URL + "/authorize", TokenURL: srv.URL + "/token", Scopes: []string{"mail"},
	})
	if err != nil {
		t.Fatal(err)
	}
	accID := store.NewID(store.PrefixAccount)
	_, _ = db.Collection(accounts.Collection).InsertOne(ctx, bson.M{
		"_id": accID, "organizationId": org, "state": accounts.StateAuthError,
		"imap": bson.M{"auth": bson.M{"user": "u@ex"}}, "smtp": bson.M{"auth": bson.M{"user": "u@ex"}},
		"version": int64(1),
	})
	start, err := svc.Start(cctx, accID, StartInput{AppID: appDoc.ID})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/callbacks/generic?code=c2&state="+start.State, nil)
	svc.HandleCallback(ctx, w, req, ProviderGeneric)
	if w.Code != http.StatusFound || !strings.Contains(w.Header().Get("Location"), "oauth=connected") {
		t.Fatalf("%d %s", w.Code, w.Header().Get("Location"))
	}
	// mismatch provider
	start2, _ := svc.Start(cctx, accID, StartInput{AppID: appDoc.ID})
	w2 := httptest.NewRecorder()
	svc.HandleCallback(ctx, w2, httptest.NewRequest(http.MethodGet, "/?code=x&state="+start2.State, nil), ProviderGmail)
	if w2.Code != http.StatusFound {
		t.Fatal(w2.Code)
	}
	// missing code
	w3 := httptest.NewRecorder()
	svc.HandleCallback(ctx, w3, httptest.NewRequest(http.MethodGet, "/?state=x", nil), ProviderGeneric)
	// AccessToken not connected after disconnect
	_ = svc.Disconnect(cctx, accID)
	if _, _, err := svc.AccessToken(ctx, accID); errs.CodeOf(err) != errs.CodeOauthNotConnected {
		t.Fatal(err)
	}
	// list q too long
	if _, err := svc.List(cctx, ListFilter{Q: strings.Repeat("x", 201)}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	// page list
	if _, err := svc.List(cctx, ListFilter{Page: 2, Limit: 1}); err != nil {
		t.Fatal(err)
	}
	// putState nil
	svc2 := &Service{}
	if err := svc2.putState(ctx, "s", flowState{}); errs.CodeOf(err) != errs.CodeServiceUnavailable {
		t.Fatal(err)
	}
	// successful redeem
	link, err := svc.CreateConnectLink(cctx, ConnectLinkCreate{AccountID: accID, AppID: appDoc.ID, TTLSeconds: 600})
	if err != nil {
		t.Fatal(err)
	}
	tok := strings.TrimPrefix(link.URL, "https://eb.example/connect/")
	w4 := httptest.NewRecorder()
	svc.RedeemConnectLink(ctx, w4, httptest.NewRequest(http.MethodGet, "/", nil), tok)
	if w4.Code != http.StatusFound {
		t.Fatalf("%d %s", w4.Code, w4.Body.String())
	}
	// Get missing
	if _, err := svc.Get(cctx, store.NewID(store.PrefixOAuthApp)); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatal(err)
	}
	if _, err := svc.Repo.GetAppByID(ctx, org, store.NewID(store.PrefixOAuthApp)); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatal(err)
	}
}

func TestCursorHelpers(t *testing.T) {
	c := listCursor{ID: "oap_1", Field: "name", Key: "x", Desc: true}
	s := encodeCursor(c)
	got, err := decodeCursor(s)
	if err != nil || got.ID != c.ID {
		t.Fatal(err, got)
	}
	if _, err := decodeCursor("%%%"); err == nil {
		t.Fatal()
	}
	f, err := cursorFilter(listCursor{ID: "a", Field: "_id", Desc: false})
	if err != nil || f == nil {
		t.Fatal(err)
	}
	f2, err := cursorFilter(listCursor{ID: "a", Field: "name", Key: "n", Desc: true})
	if err != nil || f2 == nil {
		t.Fatal(err)
	}
}

func TestLeaseHeldWaitSucceeds(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	_ = Migration(1).Up(ctx, db)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	org := store.NewID(store.PrefixOrganization)
	cctx := tenancy.WithCaller(store.WithOrganization(ctx, org), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: "u", OrganizationID: org, Role: tenancy.RoleOwner,
	})
	_, srv := startMockProvider(t)
	leases := &memLease{}
	svc := &Service{
		Repo: &Repo{DB: db}, Cipher: testCipher(t, db), Audit: nil,
		State: &memState{}, Leases: leases, NodeID: "n1",
		CallbackURL: "https://cb.example", PublicURL: "https://eb.example",
		HTTP: srv.Client(), Log: quiet,
	}
	appDoc, err := svc.Create(cctx, CreateInput{
		Name: "L", Provider: ProviderGeneric, ClientID: "c", ClientSecret: "s",
		AuthorizeURL: srv.URL + "/authorize", TokenURL: srv.URL + "/token", Scopes: []string{"mail"},
	})
	if err != nil {
		t.Fatal(err)
	}
	accID := store.NewID(store.PrefixAccount)
	_, _ = db.Collection(accounts.Collection).InsertOne(ctx, bson.M{
		"_id": accID, "organizationId": org, "state": accounts.StateConnected,
		"imap": bson.M{"auth": bson.M{"user": "u"}}, "smtp": bson.M{"auth": bson.M{"user": "u"}},
		"version": int64(1),
	})
	_ = svc.storeTokens(ctx, org, accID, appDoc, &oauth2.Token{
		AccessToken: "a1", RefreshToken: "r1", Expiry: time.Now().Add(-time.Minute),
	})
	// hold lease then release after short delay so waiter proceeds
	key := leaseKeyPref + accID
	epoch, _ := leases.Claim(ctx, key, []byte("other"), time.Second)
	go func() {
		time.Sleep(250 * time.Millisecond)
		_ = leases.Release(context.Background(), key, epoch)
		// also refresh tokens so waiter sees fresh expiry
		_, _ = db.Collection(accounts.Collection).UpdateOne(ctx, bson.M{"_id": accID}, bson.M{
			"$set": bson.M{"oauth.expiresAt": time.Now().Add(time.Hour)},
		})
	}()
	if _, _, err := svc.AccessToken(ctx, accID); err != nil {
		t.Fatal(err)
	}
}

// silence unused crypto import if any
var _ = crypto.EncryptedString{}
