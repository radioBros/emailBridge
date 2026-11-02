//go:build integration

package oauth

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/accounts"
	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

type linkFlowEnv struct {
	db    *store.DB
	svc   *Service
	org   string
	cctx  context.Context
	appID string
	accID string
}

func newLinkFlowEnv(t *testing.T) *linkFlowEnv {
	t.Helper()
	db := testDB(t)
	ctx := context.Background()
	if err := Migration(1).Up(ctx, db); err != nil {
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
	app, err := svc.Create(cctx, CreateInput{
		Name: "Generic", Provider: ProviderGeneric, ClientID: "cid", ClientSecret: "fake-client-secret",
		AuthorizeURL: srv.URL + "/authorize", TokenURL: srv.URL + "/token", Scopes: []string{"mail"},
	})
	if err != nil {
		t.Fatal(err)
	}
	accID := seedAccount(t, db, org, "owner@ex.test")
	return &linkFlowEnv{db: db, svc: svc, org: org, cctx: cctx, appID: app.ID, accID: accID}
}

func seedAccount(t *testing.T, db *store.DB, org, email string) string {
	t.Helper()
	id := store.NewID(store.PrefixAccount)
	if _, err := db.Collection(accounts.Collection).InsertOne(context.Background(), bson.M{
		"_id": id, "organizationId": org, "name": "A", "email": email,
		"state": accounts.StateNew, "imap": bson.M{"auth": bson.M{"user": email}},
		"smtp": bson.M{"auth": bson.M{"user": email}}, "createdAt": time.Now().UTC(),
		"updatedAt": time.Now().UTC(), "version": int64(1),
	}); err != nil {
		t.Fatal(err)
	}
	return id
}

func (e *linkFlowEnv) insertLink(t *testing.T, mode, accountID string) string {
	t.Helper()
	id := store.NewID(store.PrefixConnectLink)
	doc := bson.M{
		"_id": id, "organizationId": e.org, "appId": e.appID, "tokenHash": "x",
		"expiresAt": time.Now().Add(time.Hour), "createdAt": time.Now(),
	}
	if mode != "" {
		doc["mode"] = mode
	}
	if accountID != "" {
		doc["accountId"] = accountID
	}
	if _, err := e.db.Collection("connectLinks").InsertOne(context.Background(), doc); err != nil {
		t.Fatal(err)
	}
	return id
}

func (e *linkFlowEnv) usedAt(t *testing.T, id string) *time.Time {
	t.Helper()
	var l ConnectLink
	if err := e.db.Collection("connectLinks").FindOne(context.Background(), bson.M{"_id": id}).Decode(&l); err != nil {
		t.Fatal(err)
	}
	return l.UsedAt
}

func (e *linkFlowEnv) callback(t *testing.T, authorizeURL, code string) string {
	t.Helper()
	u, err := url.Parse(authorizeURL)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/callbacks/generic?code="+code+"&state="+u.Query().Get("state"), nil)
	w := httptest.NewRecorder()
	e.svc.HandleCallback(context.Background(), w, req, ProviderGeneric)
	if w.Code != http.StatusFound {
		t.Fatalf("callback %d", w.Code)
	}
	return w.Header().Get("Location")
}

// The hosted-page OAuth flow marks its link used only after a successful callback (idempotently) and
// returns the browser to the link's returnUrl.
func TestIntegrationStartForLinkMarksUsedOnSuccess(t *testing.T) {
	e := newLinkFlowEnv(t)
	sys := tenancy.WithCaller(context.Background(), tenancy.Caller{Kind: tenancy.CallerSystem, OrganizationID: e.org})
	linkID := e.insertLink(t, "", e.accID)
	ret := "https://app.example.com/done?ok=1"

	if _, err := e.svc.StartForLink(sys, e.accID, e.appID, "not-a-link", ret); errs.CodeOf(err) != errs.CodeConnectLinkInvalid {
		t.Fatalf("bad link id: %v", err)
	}
	first, err := e.svc.StartForLink(sys, e.accID, e.appID, linkID, ret)
	if err != nil {
		t.Fatal(err)
	}
	second, err := e.svc.StartForLink(sys, e.accID, e.appID, linkID, ret) // user clicked twice
	if err != nil {
		t.Fatal(err)
	}
	if e.usedAt(t, linkID) != nil {
		t.Fatal("link must stay unused until the callback succeeds")
	}
	// A failed consent does not burn the link.
	w := httptest.NewRecorder()
	e.svc.HandleCallback(context.Background(), w, httptest.NewRequest(http.MethodGet, "/callbacks/generic?error=access_denied", nil), ProviderGeneric)
	if e.usedAt(t, linkID) != nil {
		t.Fatal("failed consent must not mark the link")
	}
	if loc := e.callback(t, first.AuthorizeURL, "c1"); loc != ret {
		t.Fatalf("redirect = %s, want returnUrl", loc)
	}
	used := e.usedAt(t, linkID)
	if used == nil {
		t.Fatal("link must be used after success")
	}
	// Completing the second flow is idempotent: no error, usedAt unchanged.
	if loc := e.callback(t, second.AuthorizeURL, "c2"); loc != ret {
		t.Fatalf("second redirect = %s", loc)
	}
	if again := e.usedAt(t, linkID); again == nil || !again.Equal(*used) {
		t.Fatalf("usedAt changed: %v → %v", used, again)
	}

	// Cross-tenant: an account of another organisation is never reachable through this org's flow.
	other := seedAccount(t, e.db, store.NewID(store.PrefixOrganization), "other@ex.test")
	if _, err := e.svc.StartForLink(sys, other, e.appID, linkID, ret); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatalf("cross-tenant account: %v", err)
	}
	// Plain Start keeps the default destination.
	plain, err := e.svc.Start(e.cctx, e.accID, StartInput{AppID: e.appID})
	if err != nil {
		t.Fatal(err)
	}
	if loc := e.callback(t, plain.AuthorizeURL, "c3"); !strings.Contains(loc, "/accounts/"+e.accID+"?oauth=connected") {
		t.Fatalf("plain redirect = %s", loc)
	}
}

// Redeem checks the app (and the link mode) before consuming the single-use link.
func TestIntegrationRedeemChecksBeforeMarking(t *testing.T) {
	e := newLinkFlowEnv(t)
	link, err := e.svc.CreateConnectLink(e.cctx, ConnectLinkCreate{AccountID: e.accID, AppID: e.appID, TTLSeconds: 120})
	if err != nil {
		t.Fatal(err)
	}
	token := strings.TrimPrefix(link.URL, "https://eb.example/connect/")
	if _, err := e.db.Collection(CollectionApps).UpdateOne(context.Background(), bson.M{"_id": e.appID},
		bson.M{"$set": bson.M{"enabled": false}}); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	e.svc.RedeemConnectLink(context.Background(), w, httptest.NewRequest(http.MethodGet, "/", nil), token)
	if w.Code == http.StatusFound {
		t.Fatal("disabled app must not redirect")
	}
	if e.usedAt(t, link.ID) != nil {
		t.Fatal("a disabled app must not consume the link")
	}
	if _, err := e.db.Collection(CollectionApps).UpdateOne(context.Background(), bson.M{"_id": e.appID},
		bson.M{"$set": bson.M{"enabled": true}}); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	e.svc.RedeemConnectLink(context.Background(), w, httptest.NewRequest(http.MethodGet, "/", nil), token)
	if w.Code != http.StatusFound || e.usedAt(t, link.ID) == nil {
		t.Fatalf("redeem %d", w.Code)
	}

	// A hosted-page link (mode hosted, no account) is never redeemable here.
	hosted, err := e.svc.CreateConnectLink(e.cctx, ConnectLinkCreate{AccountID: e.accID, AppID: e.appID, TTLSeconds: 120})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.db.Collection("connectLinks").UpdateOne(context.Background(), bson.M{"_id": hosted.ID},
		bson.M{"$set": bson.M{"mode": "hosted"}}); err != nil {
		t.Fatal(err)
	}
	w = httptest.NewRecorder()
	e.svc.RedeemConnectLink(context.Background(), w, httptest.NewRequest(http.MethodGet, "/", nil),
		strings.TrimPrefix(hosted.URL, "https://eb.example/connect/"))
	if w.Code == http.StatusFound || e.usedAt(t, hosted.ID) != nil {
		t.Fatalf("hosted link redeemed: %d", w.Code)
	}
}
