//go:build integration

package tracking

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/events"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/store"
)

func testDB(t *testing.T) *store.DB {
	t.Helper()
	uri := os.Getenv("EB_TEST_MONGO_URI")
	if uri == "" {
		uri = "mongodb://127.0.0.1:27017/?replicaSet=rs0&directConnection=true"
	}
	ctx := context.Background()
	db, err := store.Connect(ctx, store.Options{
		URI: uri, Database: fmt.Sprintf("ebtest_trk_%d", time.Now().UnixNano()), ConnectTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Skipf("no MongoDB: %v", err)
	}
	t.Cleanup(func() { _ = db.DB.Drop(ctx); _ = db.Close(ctx) })
	return db
}

func TestIntegrationApplyClickOpenUnsub(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	if err := Migration(1).Up(ctx, db); err != nil {
		t.Fatal(err)
	}
	key := []byte("integration-tracking-key-32byte!")
	org := store.NewID(store.PrefixOrganization)
	acc := store.NewID(store.PrefixAccount)
	out := store.NewID(store.PrefixOutbound)
	unsubHits := 0
	ew := &events.Writer{DB: db, Partitions: 1, Retention: func() time.Duration { return time.Hour }}
	svc := &Service{
		Repo: &Repo{DB: db}, SignKey: key, PublicURL: "https://eb.example",
		TTL: time.Hour, Registry: features.DefaultRegistry(), Env: "test", Events: ew,
		Unsub: func(_ context.Context, o, a, _, _ string) error {
			unsubHits++
			if o != org || a != "u@example.test" {
				t.Fatalf("%s %s", o, a)
			}
			return nil
		},
		AccountOf: func(context.Context, string, string) (string, *string) { return acc, nil },
	}
	_, _ = db.Collection("accounts").InsertOne(ctx, map[string]any{
		"_id": acc, "organizationId": org,
		"switches": map[string]any{"tracking.opens": true, "tracking.clicks": true, "sending.listUnsubscribe": true},
	})
	opens, clicks, unsub := svc.SwitchState(ctx, org, acc)
	if !opens || !clicks || !unsub {
		t.Fatalf("switches %v %v %v", opens, clicks, unsub)
	}
	res, err := svc.Apply(ctx, PrepareInput{
		OrgID: org, AccountID: acc, OutboundID: out, Recipient: "u@example.test",
		HTML:    `<html><body><a href="https://dest.example/path">go</a></body></html>`,
		Headers: map[string]string{}, TrackOpens: true, TrackClicks: true, ListUnsub: true,
	})
	if err != nil || len(res.Links) != 1 || !strings.Contains(res.HTML, "/t/o/") {
		t.Fatalf("%+v %v", res, err)
	}
	exp := time.Now().UTC().Add(time.Hour)
	clickTok, _ := Sign(key, Claims{Kind: KindClick, OrgID: org, OutboundID: out, Recipient: "u@example.test", Index: 0, Exp: exp})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/t/c/"+clickTok, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0")
	svc.HandleClick(rr, req, clickTok)
	if rr.Code != http.StatusFound || rr.Header().Get("Location") != "https://dest.example/path" {
		t.Fatalf("click: %d %s", rr.Code, rr.Header().Get("Location"))
	}
	openTok, _ := Sign(key, Claims{Kind: KindOpen, OrgID: org, OutboundID: out, Recipient: "u@example.test", Exp: exp})
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/t/o/"+openTok, nil)
	req.Header.Set("User-Agent", "GoogleImageProxy/1.0")
	svc.HandleOpen(rr, req, openTok)
	if rr.Code != 200 {
		t.Fatal(rr.Code)
	}
	unsubTok, _ := Sign(key, Claims{Kind: KindUnsub, OrgID: org, OutboundID: out, Recipient: "u@example.test", Exp: exp})
	rr = httptest.NewRecorder()
	svc.HandleUnsubGET(rr, httptest.NewRequest(http.MethodGet, "/u/"+unsubTok, nil), unsubTok)
	if rr.Code != 200 {
		t.Fatal(rr.Code)
	}
	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/u/"+unsubTok, strings.NewReader("List-Unsubscribe=One-Click"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	svc.HandleUnsubPOST(rr, req, unsubTok)
	if rr.Code != 200 || unsubHits != 1 {
		t.Fatalf("unsub %d hits=%d", rr.Code, unsubHits)
	}
}

func TestIntegrationModuleStart(t *testing.T) {
	db := testDB(t)
	_ = EnsureIndexes(context.Background(), db)
	key := []byte("module-tracking-key-32-bytes!!!!")
	a := app.New(app.Options{Roles: app.RoleSet{app.RoleAPI: true}, NodeID: "trk-n1", Log: slog.Default()})
	app.Provide(a, db)
	srv, err := httpapi.New(httpapi.Options{PublicURL: "https://eb.example"})
	if err != nil {
		t.Fatal(err)
	}
	app.Provide(a, srv)
	m := &Module{SignKey: key, PublicURL: "https://eb.example", Registry: features.DefaultRegistry(), Env: "test", Log: slog.Default()}
	if m.Name() != "tracking" || len(m.Roles()) == 0 {
		t.Fatal("meta")
	}
	if err := m.Start(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Stop(context.Background()) }()
	if m.Service() == nil {
		t.Fatal("nil svc")
	}
	org := store.NewID(store.PrefixOrganization)
	out := store.NewID(store.PrefixOutbound)
	tok, _ := Sign(key, Claims{
		Kind: KindOpen, OrgID: org, OutboundID: out, Recipient: "a@b.test",
		Exp: time.Now().UTC().Add(time.Hour),
	})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/t/o/"+tok, nil)
	srv.ServeHTTP(rr, req)
	if rr.Code != 200 {
		t.Fatalf("%d %s", rr.Code, rr.Body.String())
	}
	// click missing link
	ctok, _ := Sign(key, Claims{Kind: KindClick, OrgID: org, OutboundID: out, Recipient: "a@b.test", Index: 9, Exp: time.Now().UTC().Add(time.Hour)})
	rr = httptest.NewRecorder()
	srv.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/t/c/"+ctok, nil))
	if rr.Code == 302 {
		t.Fatal("expected missing")
	}
}
