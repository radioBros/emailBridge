package graph_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/providers/graph"
	"github.com/radiobros/emailbridge/internal/store"
)

func TestCallback_mongoLookupAcrossNodes(t *testing.T) {
	uri := "mongodb://127.0.0.1:27017/?replicaSet=rs0&directConnection=true"
	ctx := context.Background()
	db, err := store.Connect(ctx, store.Options{
		URI: uri, Database: fmt.Sprintf("eb_graph_cb_%d", time.Now().UnixNano()), ConnectTimeout: 3 * time.Second,
	})
	if err != nil {
		t.Skipf("mongo: %v", err)
	}
	t.Cleanup(func() { _ = db.DB.Drop(ctx); _ = db.Close(ctx) })
	if err := graph.Migration(35).Up(ctx, db); err != nil {
		t.Fatal(err)
	}
	repo := &graph.StateRepo{DB: db}
	accID := store.NewID(store.PrefixAccount)
	st := &graph.SyncState{
		AccountID: accID, OrganizationID: store.NewID(store.PrefixOrganization),
		SubscriptionID: "sub-cross", ClientState: "client-secret-ok", UIDValidity: 1,
	}
	if err := repo.Save(ctx, st); err != nil {
		t.Fatal(err)
	}

	var nudged string
	// Fresh handler = "different api node" (empty in-memory maps, state only in Mongo).
	h := &graph.CallbackHandler{
		Repo: repo,
		Nudge: nudgeFn(func(_ context.Context, accountID string) error {
			nudged = accountID
			return nil
		}),
	}
	body := `{"value":[{"subscriptionId":"sub-cross","clientState":"client-secret-ok","changeType":"created"}]}`
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/callbacks/graph", strings.NewReader(body)))
	if rec.Code != 200 {
		t.Fatalf("valid notification: %d %s", rec.Code, rec.Body.String())
	}
	if nudged != accID {
		t.Fatalf("nudge account got %q want %q", nudged, accID)
	}

	rec2 := httptest.NewRecorder()
	bad := `{"value":[{"subscriptionId":"sub-cross","clientState":"wrong","changeType":"created"}]}`
	h2 := &graph.CallbackHandler{Repo: repo} // another fresh node
	h2.ServeHTTP(rec2, httptest.NewRequest(http.MethodPost, "/callbacks/graph", strings.NewReader(bad)))
	if rec2.Code != http.StatusUnauthorized {
		t.Fatalf("wrong clientState: %d", rec2.Code)
	}
}

func TestNotificationURLFromCallback(t *testing.T) {
	if u := graph.NotificationURLFromCallback("https://eb.example/"); u != "https://eb.example/callbacks/graph" {
		t.Fatal(u)
	}
	if graph.NotificationURLFromCallback("") != "" {
		t.Fatal("empty")
	}
}
