//go:build integration

package oauth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"golang.org/x/oauth2"

	"github.com/radiobros/emailbridge/internal/accounts"
	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
	simgmail "github.com/radiobros/emailbridge/simulator/gmail"
	simgraph "github.com/radiobros/emailbridge/simulator/graph"
)

func testSAJSON(t *testing.T, tokenURI string) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	b, err := json.Marshal(map[string]string{
		"type": "service_account", "client_email": "sa@project.iam.gserviceaccount.com",
		"private_key": string(pemBytes), "token_uri": tokenURI,
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func insertAccount(t *testing.T, db *store.DB, org, id, email, provider string) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Millisecond)
	_, err := db.Collection(accounts.Collection).InsertOne(context.Background(), bson.M{
		"_id": id, "organizationId": org, "name": "A", "email": email,
		"provider": provider, "state": accounts.StateNew,
		"imap":      bson.M{"host": "", "port": 0, "tls": "tls", "auth": bson.M{"user": ""}},
		"smtp":      bson.M{"host": "", "port": 0, "tls": "tls", "auth": bson.M{"user": ""}},
		"sync":      bson.M{"paths": []string{"*"}, "idlePaths": []string{"INBOX"}},
		"createdAt": now, "updatedAt": now, "version": 1,
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestW43_GmailServiceAccount_linkTokenCacheRevoke(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	org := store.NewID(store.PrefixOrganization)
	cctx := tenancy.WithCaller(store.WithOrganization(ctx, org), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: store.NewID(store.PrefixUser), OrganizationID: org, Role: tenancy.RoleOwner,
	})
	sim := simgmail.New()
	t.Cleanup(sim.Close)
	mailbox := "user@ex.com"
	sim.EnsureUser(mailbox)

	svc := &Service{
		Repo: &Repo{DB: db}, Cipher: testCipher(t, db),
		Audit:  &audit.Writer{DB: db, Log: quiet, Retention: time.Hour},
		Leases: &memLease{}, NodeID: "n1",
		HTTP: http.DefaultClient, Log: quiet,
		GmailAPIRoot: sim.URL(),
	}

	sk := testSAJSON(t, sim.URL()+"/token")
	app, err := svc.Create(cctx, CreateInput{
		Name: "G-SA", Provider: ProviderGmail, ClientID: "cid", ClientSecret: "sec",
		ServiceKey: sk, TokenURL: sim.URL() + "/token",
	})
	if err != nil {
		t.Fatal(err)
	}

	accID := store.NewID(store.PrefixAccount)
	insertAccount(t, db, org, accID, mailbox, accounts.ProviderGmail)

	if err := svc.LinkAppOnly(cctx, accID, app.ID, false); err != nil {
		t.Fatal(err)
	}
	var linked struct {
		OAuth *AccountOAuth `bson:"oauth"`
		State string        `bson:"state"`
		IMAP  struct {
			Auth struct {
				User string `bson:"user"`
			} `bson:"auth"`
		} `bson:"imap"`
	}
	if err := db.Collection(accounts.Collection).FindOne(ctx, bson.M{"_id": accID}).Decode(&linked); err != nil {
		t.Fatal(err)
	}
	if linked.OAuth == nil || linked.OAuth.Mode != AuthModeServiceAccount || linked.OAuth.AccessToken.IsZero() {
		t.Fatalf("%+v", linked.OAuth)
	}
	if linked.IMAP.Auth.User != mailbox {
		t.Fatalf("user=%q", linked.IMAP.Auth.User)
	}

	tok1, user, err := svc.AccessToken(cctx, accID)
	if err != nil || tok1 == "" || user != mailbox {
		t.Fatalf("%v %q %q", err, tok1, user)
	}
	tok2, _, err := svc.AccessToken(cctx, accID)
	if err != nil || tok2 != tok1 {
		t.Fatalf("cache miss: %v %q %q", err, tok1, tok2)
	}

	// Force remint: expire cached token.
	_, _ = db.Collection(accounts.Collection).UpdateOne(ctx, bson.M{"_id": accID}, bson.M{
		"$set": bson.M{"oauth.expiresAt": time.Now().UTC().Add(-time.Minute)},
	})
	tok3, _, err := svc.AccessToken(cctx, accID)
	if err != nil || tok3 == "" || tok3 == tok1 {
		t.Fatalf("remint: %v %q vs %q", err, tok3, tok1)
	}

	// Clear service key → next AccessToken → authError.
	empty := ""
	if _, err := svc.Patch(cctx, app.ID, formatETag(app.Version), PatchInput{ServiceKey: &empty}); err != nil {
		// reload version
		app2, _ := svc.Get(cctx, app.ID)
		if _, err := svc.Patch(cctx, app.ID, formatETag(app2.Version), PatchInput{ServiceKey: &empty}); err != nil {
			t.Fatal(err)
		}
	}
	_, _ = db.Collection(accounts.Collection).UpdateOne(ctx, bson.M{"_id": accID}, bson.M{
		"$set": bson.M{"oauth.expiresAt": time.Now().UTC().Add(-time.Minute)},
	})
	if _, _, err := svc.AccessToken(cctx, accID); errs.CodeOf(err) != errs.CodeOauthRevoked {
		t.Fatalf("want revoked: %v", err)
	}
	var st struct {
		State       string                `bson:"state"`
		StateReason *accounts.StateReason `bson:"stateReason"`
	}
	_ = db.Collection(accounts.Collection).FindOne(ctx, bson.M{"_id": accID}).Decode(&st)
	if st.State != accounts.StateAuthError || st.StateReason == nil {
		t.Fatalf("%+v", st)
	}
}

func TestW43_GraphAppOnly_linkAndRevoke(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	org := store.NewID(store.PrefixOrganization)
	cctx := tenancy.WithCaller(store.WithOrganization(ctx, org), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: store.NewID(store.PrefixUser), OrganizationID: org, Role: tenancy.RoleOwner,
	})
	sim := simgraph.New()
	t.Cleanup(sim.Close)
	mailbox := "graph-user@ex.com"
	sim.EnsureUser(mailbox)

	svc := &Service{
		Repo: &Repo{DB: db}, Cipher: testCipher(t, db),
		Audit:  &audit.Writer{DB: db, Log: quiet, Retention: time.Hour},
		Leases: &memLease{}, NodeID: "n1",
		HTTP: http.DefaultClient, Log: quiet,
		GraphAPIRoot: sim.URL(),
	}
	ao := true
	app, err := svc.Create(cctx, CreateInput{
		Name: "G-AO", Provider: ProviderOutlook, ClientID: "cid", ClientSecret: "sec",
		AppOnly: &ao, TenantID: "common", TokenURL: sim.URL() + "/token",
	})
	if err != nil {
		t.Fatal(err)
	}
	accID := store.NewID(store.PrefixAccount)
	insertAccount(t, db, org, accID, mailbox, accounts.ProviderGraph)

	if err := svc.LinkAppOnly(cctx, accID, app.ID, false); err != nil {
		t.Fatal(err)
	}
	tok, user, err := svc.AccessToken(cctx, accID)
	if err != nil || tok == "" || user != mailbox {
		t.Fatalf("%v %q %q", err, tok, user)
	}

	off := false
	app2, err := svc.Get(cctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Patch(cctx, app.ID, formatETag(app2.Version), PatchInput{AppOnly: &off}); err != nil {
		t.Fatal(err)
	}
	_, _ = db.Collection(accounts.Collection).UpdateOne(ctx, bson.M{"_id": accID}, bson.M{
		"$set": bson.M{"oauth.expiresAt": time.Now().UTC().Add(-time.Minute)},
	})
	if _, _, err := svc.AccessToken(cctx, accID); errs.CodeOf(err) != errs.CodeOauthRevoked {
		t.Fatalf("want revoked: %v", err)
	}
}

func TestW43_LinkRefusalAndVerifyFail(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	org := store.NewID(store.PrefixOrganization)
	cctx := tenancy.WithCaller(store.WithOrganization(ctx, org), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: store.NewID(store.PrefixUser), OrganizationID: org, Role: tenancy.RoleOwner,
	})
	sim := simgmail.New()
	t.Cleanup(sim.Close)

	svc := &Service{
		Repo: &Repo{DB: db}, Cipher: testCipher(t, db),
		Audit:  &audit.Writer{DB: db, Log: quiet, Retention: time.Hour},
		Leases: &memLease{}, NodeID: "n1",
		HTTP: http.DefaultClient, Log: quiet, GmailAPIRoot: sim.URL(),
	}
	sk := testSAJSON(t, sim.URL()+"/token")
	app, err := svc.Create(cctx, CreateInput{
		Name: "G", Provider: ProviderGmail, ClientID: "c", ClientSecret: "s", ServiceKey: sk,
		TokenURL: sim.URL() + "/token",
	})
	if err != nil {
		t.Fatal(err)
	}
	noSA, err := svc.Create(cctx, CreateInput{
		Name: "NoSA", Provider: ProviderGmail, ClientID: "c2", ClientSecret: "s2",
	})
	if err != nil {
		t.Fatal(err)
	}
	ao := true
	outlook, err := svc.Create(cctx, CreateInput{
		Name: "O", Provider: ProviderOutlook, ClientID: "c3", ClientSecret: "s3", AppOnly: &ao,
	})
	if err != nil {
		t.Fatal(err)
	}

	gmailAcc := store.NewID(store.PrefixAccount)
	insertAccount(t, db, org, gmailAcc, "a@ex.com", accounts.ProviderGmail)
	imapAcc := store.NewID(store.PrefixAccount)
	insertAccount(t, db, org, imapAcc, "b@ex.com", accounts.ProviderIMAP)

	if err := svc.LinkAppOnly(cctx, gmailAcc, noSA.ID, true); errs.CodeOf(err) != errs.CodeOauthAppLinkInvalid {
		t.Fatalf("no key: %v", err)
	}
	if err := svc.LinkAppOnly(cctx, gmailAcc, outlook.ID, true); errs.CodeOf(err) != errs.CodeOauthAppLinkInvalid {
		t.Fatalf("wrong app: %v", err)
	}
	if err := svc.LinkAppOnly(cctx, imapAcc, app.ID, true); errs.CodeOf(err) != errs.CodeOauthAppLinkInvalid {
		t.Fatalf("imap: %v", err)
	}

	// Verify failure: wrong mailbox email vs token subject — use unreachable API root.
	svc.GmailAPIRoot = "http://127.0.0.1:1"
	badAcc := store.NewID(store.PrefixAccount)
	insertAccount(t, db, org, badAcc, "fail@ex.com", accounts.ProviderGmail)
	if err := svc.LinkAppOnly(cctx, badAcc, app.ID, false); err == nil {
		t.Fatal("expected verify fail")
	}
	var gone struct {
		OAuth *AccountOAuth `bson:"oauth"`
	}
	_ = db.Collection(accounts.Collection).FindOne(ctx, bson.M{"_id": badAcc}).Decode(&gone)
	if gone.OAuth != nil {
		t.Fatal("oauth should be rolled back")
	}

	// skipVerify succeeds without probe.
	svc.GmailAPIRoot = sim.URL()
	if err := svc.LinkAppOnly(cctx, gmailAcc, app.ID, true); err != nil {
		t.Fatal(err)
	}

	appID := app.ID
	// Clear account bindings first (Delete refuses while oauth.appId still references the app).
	_, _ = db.Collection(accounts.Collection).UpdateMany(ctx, bson.M{"organizationId": org, "oauth.appId": appID},
		bson.M{"$unset": bson.M{"oauth": ""}})
	app3, _ := svc.Get(cctx, appID)
	if err := svc.Delete(cctx, appID, formatETag(app3.Version)); err != nil {
		t.Fatal(err)
	}
	// Re-bind so AccessToken hits the missing app (revoked / not connected path).
	_, _ = db.Collection(accounts.Collection).UpdateOne(ctx, bson.M{"_id": gmailAcc}, bson.M{
		"$set": bson.M{
			"oauth": bson.M{"appId": appID, "expiresAt": time.Now().UTC().Add(-time.Minute)},
		},
	})
	if _, _, err := svc.AccessToken(cctx, gmailAcc); errs.CodeOf(err) != errs.CodeOauthRevoked && errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatalf("deleted app: %v", err)
	}
}

func TestW43_LinkDisabledAndMismatchAndLeaseNil(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	org := store.NewID(store.PrefixOrganization)
	cctx := tenancy.WithCaller(store.WithOrganization(ctx, org), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: store.NewID(store.PrefixUser), OrganizationID: org, Role: tenancy.RoleOwner,
	})
	sim := simgmail.New()
	t.Cleanup(sim.Close)
	sim.EnsureUser("real@ex.com")

	svc := &Service{
		Repo: &Repo{DB: db}, Cipher: testCipher(t, db),
		Audit: &audit.Writer{DB: db, Log: quiet, Retention: time.Hour},
		HTTP:  http.DefaultClient, Log: quiet, GmailAPIRoot: sim.URL(),
		// Leases nil → direct mint path
	}
	sk := testSAJSON(t, sim.URL()+"/token")
	en := false
	app, err := svc.Create(cctx, CreateInput{
		Name: "Off", Provider: ProviderGmail, ClientID: "c", ClientSecret: "s",
		ServiceKey: sk, TokenURL: sim.URL() + "/token", Enabled: &en,
	})
	if err != nil {
		t.Fatal(err)
	}
	acc := store.NewID(store.PrefixAccount)
	insertAccount(t, db, org, acc, "real@ex.com", accounts.ProviderGmail)
	if err := svc.LinkAppOnly(cctx, acc, app.ID, true); errs.CodeOf(err) != errs.CodeOauthAppDisabled {
		t.Fatalf("%v", err)
	}

	on := true
	app, err = svc.Patch(cctx, app.ID, formatETag(app.Version), PatchInput{Enabled: &on})
	if err != nil {
		t.Fatal(err)
	}
	tok, _, _, err := svc.mintAppOnly(cctx, app, AuthModeServiceAccount, "real@ex.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.probeAppOnly(cctx, AuthModeServiceAccount, "other@ex.com", tok); errs.CodeOf(err) != errs.CodeOauthAppLinkInvalid {
		t.Fatalf("mismatch: %v", err)
	}

	if err := svc.LinkAppOnly(cctx, acc, app.ID, false); err != nil {
		t.Fatal(err)
	}
	_, _ = db.Collection(accounts.Collection).UpdateOne(ctx, bson.M{"_id": acc}, bson.M{
		"$set": bson.M{"oauth.expiresAt": time.Now().UTC().Add(-time.Minute)},
	})
	if _, _, err := svc.AccessToken(cctx, acc); err != nil {
		t.Fatal(err)
	}

	// Disabled app on remint.
	app2, _ := svc.Get(cctx, app.ID)
	off := false
	if _, err := svc.Patch(cctx, app.ID, formatETag(app2.Version), PatchInput{Enabled: &off}); err != nil {
		t.Fatal(err)
	}
	_, _ = db.Collection(accounts.Collection).UpdateOne(ctx, bson.M{"_id": acc}, bson.M{
		"$set": bson.M{"oauth.expiresAt": time.Now().UTC().Add(-time.Minute)},
	})
	if _, _, err := svc.AccessToken(cctx, acc); errs.CodeOf(err) != errs.CodeOauthRevoked {
		t.Fatalf("%v", err)
	}
}

func TestW43_LinkCrossOrgEmptyEmailStoreMiss(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	org := store.NewID(store.PrefixOrganization)
	org2 := store.NewID(store.PrefixOrganization)
	cctx := tenancy.WithCaller(store.WithOrganization(ctx, org), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: store.NewID(store.PrefixUser), OrganizationID: org, Role: tenancy.RoleOwner,
	})
	sim := simgmail.New()
	t.Cleanup(sim.Close)
	svc := &Service{
		Repo: &Repo{DB: db}, Cipher: testCipher(t, db),
		Audit: &audit.Writer{DB: db, Log: quiet, Retention: time.Hour},
		HTTP:  http.DefaultClient, Log: quiet, GmailAPIRoot: sim.URL(),
	}
	sk := testSAJSON(t, sim.URL()+"/token")
	// Create app in org2 by swapping org context briefly.
	c2 := tenancy.WithCaller(store.WithOrganization(ctx, org2), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: store.NewID(store.PrefixUser), OrganizationID: org2, Role: tenancy.RoleOwner,
	})
	appOther, err := svc.Create(c2, CreateInput{
		Name: "OtherOrg", Provider: ProviderGmail, ClientID: "c", ClientSecret: "s", ServiceKey: sk,
	})
	if err != nil {
		t.Fatal(err)
	}
	acc := store.NewID(store.PrefixAccount)
	insertAccount(t, db, org, acc, "x@ex.com", accounts.ProviderGmail)
	if err := svc.LinkAppOnly(cctx, acc, appOther.ID, true); errs.CodeOf(err) != errs.CodeOauthAppLinkInvalid && errs.CodeOf(err) != errs.CodeNotFound {
		// getApp is org-scoped via tenancy — may be not found
		t.Log(err)
		if err == nil {
			t.Fatal("expected refuse")
		}
	}

	emptyMail := store.NewID(store.PrefixAccount)
	now := time.Now().UTC().Truncate(time.Millisecond)
	_, _ = db.Collection(accounts.Collection).InsertOne(ctx, bson.M{
		"_id": emptyMail, "organizationId": org, "name": "E", "email": "",
		"provider": accounts.ProviderGmail, "state": accounts.StateNew,
		"imap": bson.M{"auth": bson.M{"user": ""}}, "smtp": bson.M{"auth": bson.M{"user": ""}},
		"createdAt": now, "updatedAt": now, "version": 1,
	})
	app, err := svc.Create(cctx, CreateInput{
		Name: "Local", Provider: ProviderGmail, ClientID: "c2", ClientSecret: "s2",
		ServiceKey: sk, TokenURL: sim.URL() + "/token",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.LinkAppOnly(cctx, emptyMail, app.ID, true); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("%v", err)
	}
	missing := store.NewID(store.PrefixAccount)
	if err := svc.storeAppOnlyTokens(cctx, org, missing, app, AuthModeServiceAccount, &oauth2.Token{
		AccessToken: "x", Expiry: time.Time{},
	}); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatalf("%v", err)
	}

	// Graph probe failure (bad API root).
	simG := simgraph.New()
	t.Cleanup(simG.Close)
	ao := true
	gApp, err := svc.Create(cctx, CreateInput{
		Name: "GProbe", Provider: ProviderOutlook, ClientID: "cg", ClientSecret: "sg",
		AppOnly: &ao, TokenURL: simG.URL() + "/token",
	})
	if err != nil {
		t.Fatal(err)
	}
	svc.GraphAPIRoot = "http://127.0.0.1:1"
	gAcc := store.NewID(store.PrefixAccount)
	insertAccount(t, db, org, gAcc, "g@ex.com", accounts.ProviderGraph)
	if err := svc.LinkAppOnly(cctx, gAcc, gApp.ID, false); err == nil {
		t.Fatal("expected graph probe fail")
	}
}

func TestW43_AccessToken_emptyUserAndLeaseHeld(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	org := store.NewID(store.PrefixOrganization)
	cctx := tenancy.WithCaller(store.WithOrganization(ctx, org), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: store.NewID(store.PrefixUser), OrganizationID: org, Role: tenancy.RoleOwner,
	})
	sim := simgmail.New()
	t.Cleanup(sim.Close)
	mailbox := "lease@ex.com"
	sim.EnsureUser(mailbox)

	leases := &memLease{}
	svc := &Service{
		Repo: &Repo{DB: db}, Cipher: testCipher(t, db),
		Audit:  &audit.Writer{DB: db, Log: quiet, Retention: time.Hour},
		Leases: leases, NodeID: "n1",
		HTTP: http.DefaultClient, Log: quiet, GmailAPIRoot: sim.URL(),
	}
	sk := testSAJSON(t, sim.URL()+"/token")
	app, err := svc.Create(cctx, CreateInput{
		Name: "Lease", Provider: ProviderGmail, ClientID: "c", ClientSecret: "s",
		ServiceKey: sk, TokenURL: sim.URL() + "/token",
	})
	if err != nil {
		t.Fatal(err)
	}
	accID := store.NewID(store.PrefixAccount)
	insertAccount(t, db, org, accID, mailbox, accounts.ProviderGmail)
	if err := svc.LinkAppOnly(cctx, accID, app.ID, false); err != nil {
		t.Fatal(err)
	}
	// Clear IMAP/SMTP user so AccessToken falls back to email.
	_, _ = db.Collection(accounts.Collection).UpdateOne(ctx, bson.M{"_id": accID}, bson.M{
		"$set": bson.M{"imap.auth.user": "", "smtp.auth.user": "", "oauth.expiresAt": time.Now().UTC().Add(-time.Minute)},
	})
	tok, user, err := svc.AccessToken(cctx, accID)
	if err != nil || tok == "" || user != mailbox {
		t.Fatalf("%v %q %q", err, tok, user)
	}

	// Hold lease so waiter re-reads a freshly minted token.
	_, _ = db.Collection(accounts.Collection).UpdateOne(ctx, bson.M{"_id": accID}, bson.M{
		"$set": bson.M{"oauth.expiresAt": time.Now().UTC().Add(-time.Minute)},
	})
	key := "oauth.refresh." + accID
	epoch, err := leases.Claim(ctx, key, []byte("other"), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(250 * time.Millisecond)
		_ = leases.Release(context.Background(), key, epoch)
		doc, _ := svc.loadAccountOAuth(ctx, accID)
		_ = svc.mintAppOnlyWithLease(ctx, doc, mailbox)
	}()
	if _, _, err := svc.AccessToken(cctx, accID); err != nil {
		t.Fatal(err)
	}

	// Exhaust lease retries while another holder never releases.
	_, _ = db.Collection(accounts.Collection).UpdateOne(ctx, bson.M{"_id": accID}, bson.M{
		"$set": bson.M{"oauth.expiresAt": time.Now().UTC().Add(-time.Minute)},
	})
	holdKey := "oauth.refresh." + accID
	if _, err := leases.Claim(ctx, holdKey, []byte("stuck"), time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.AccessToken(cctx, accID); errs.CodeOf(err) != errs.CodeServiceUnavailable {
		t.Fatalf("want lease exhausted: %v", err)
	}
}

func TestW43_AccountsCreateBindHook(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	org := store.NewID(store.PrefixOrganization)
	cctx := tenancy.WithCaller(store.WithOrganization(ctx, org), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: store.NewID(store.PrefixUser), OrganizationID: org, Role: tenancy.RoleOwner,
	})
	sim := simgmail.New()
	t.Cleanup(sim.Close)
	mailbox := "bind@ex.com"
	sim.EnsureUser(mailbox)

	oauthSvc := &Service{
		Repo: &Repo{DB: db}, Cipher: testCipher(t, db),
		Audit:  &audit.Writer{DB: db, Log: quiet, Retention: time.Hour},
		Leases: &memLease{}, NodeID: "n1",
		HTTP: http.DefaultClient, Log: quiet, GmailAPIRoot: sim.URL(),
	}
	sk := testSAJSON(t, sim.URL()+"/token")
	app, err := oauthSvc.Create(cctx, CreateInput{
		Name: "Bind", Provider: ProviderGmail, ClientID: "c", ClientSecret: "s",
		ServiceKey: sk, TokenURL: sim.URL() + "/token",
	})
	if err != nil {
		t.Fatal(err)
	}

	accSvc := &accounts.Service{
		DB: db, Cipher: oauthSvc.Cipher, Log: quiet,
		BindOAuthApp: oauthSvc.LinkAppOnly,
	}
	if err := accounts.EnsureIndexes(ctx, db); err != nil {
		t.Fatal(err)
	}
	a, err := accSvc.Create(cctx, accounts.CreateInput{
		Name: "Gmail SA", Email: mailbox, Provider: accounts.ProviderGmail,
		SkipVerify: true,
		OAuth:      &accounts.OAuthBindInput{AppID: app.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if a.OAuth == nil || a.OAuth.AppID != app.ID {
		t.Fatalf("%+v", a.OAuth)
	}
	tok, _, err := oauthSvc.AccessToken(cctx, a.ID)
	if err != nil || tok == "" {
		t.Fatal(err, tok)
	}

	// Nil hook.
	accSvc2 := &accounts.Service{DB: db, Cipher: oauthSvc.Cipher, Log: quiet}
	if _, err := accSvc2.Create(cctx, accounts.CreateInput{
		Name: "X", Email: "x2@ex.com", Provider: accounts.ProviderGmail,
		OAuth: &accounts.OAuthBindInput{AppID: app.ID}, SkipVerify: true,
	}); errs.CodeOf(err) != errs.CodeInternalError {
		t.Fatalf("nil hook: %v", err)
	}

	// Patch oauth bind on an unbound gmail account.
	a2, err := accSvc.Create(cctx, accounts.CreateInput{
		Name: "PatchMe", Email: "patch@ex.com", Provider: accounts.ProviderGmail, SkipVerify: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	sim.EnsureUser("patch@ex.com")
	patched, err := accSvc.Patch(cctx, a2.ID, accounts.PatchInput{
		OAuth: &accounts.OAuthBindInput{AppID: app.ID},
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if patched.OAuth == nil || patched.OAuth.AppID != app.ID {
		t.Fatalf("%+v", patched.OAuth)
	}
}
