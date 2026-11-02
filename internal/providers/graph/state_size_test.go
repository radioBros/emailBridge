package graph_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/events"
	"github.com/radiobros/emailbridge/internal/index"
	"github.com/radiobros/emailbridge/internal/providers/graph"
	"github.com/radiobros/emailbridge/internal/store"
	simgraph "github.com/radiobros/emailbridge/simulator/graph"
)

// TestSyncState_20kMessagesStaysSmall ensures per-message data is not stored in graphSyncState.
func TestSyncState_20kMessagesStaysSmall(t *testing.T) {
	uri := "mongodb://127.0.0.1:27017/?replicaSet=rs0&directConnection=true"
	ctx := context.Background()
	db, err := store.Connect(ctx, store.Options{
		URI: uri, Database: fmt.Sprintf("eb_graph_20k_%d", time.Now().UnixNano()), ConnectTimeout: 3 * time.Second,
	})
	if err != nil {
		t.Skipf("mongo: %v", err)
	}
	t.Cleanup(func() { _ = db.DB.Drop(ctx); _ = db.Close(ctx) })
	for _, m := range []store.Migration{index.Migration(1), index.ProviderIDMigration(37), graph.Migration(35)} {
		if err := m.Up(ctx, db); err != nil {
			t.Fatal(err)
		}
	}

	sim := simgraph.New()
	defer sim.Close()
	sim.SetToken("big@ex.com", "tok")
	const n = 20_000
	sim.SeedN("big@ex.com", "inbox", n)

	acc := graph.Account{
		ID: store.NewID(store.PrefixAccount), OrganizationID: store.NewID(store.PrefixOrganization),
		Email: "big@ex.com", UPN: "big@ex.com", Paths: []string{"INBOX"},
		NotifyFrom: time.Now().Add(time.Hour), // silent index (no received flood)
	}
	ix := &index.Index{DB: db}
	repo := &graph.StateRepo{DB: db}
	r := &graph.Runner{
		Client: &graph.Client{BaseURL: sim.URL()},
		Tokens: graph.StaticToken{Token: "tok", Username: "big@ex.com"},
		Repo:   repo, Index: ix, State: &graph.SyncState{},
		Config: graph.Config{SkipMIME: true},
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		Write: func(_ context.Context, _ []events.Draft, fn func(context.Context) error) error {
			if fn != nil {
				return fn(context.Background())
			}
			return nil
		},
	}
	if err := r.SyncOnce(ctx, acc); err != nil {
		t.Fatal(err)
	}
	st, err := repo.Load(ctx, &acc)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := bson.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) >= 64*1024 {
		t.Fatalf("state BSON %d bytes want < 64KiB", len(raw))
	}
	count, err := db.Collection(index.Messages).CountDocuments(ctx, bson.M{"accountId": acc.ID})
	if err != nil {
		t.Fatal(err)
	}
	if count < int64(n) {
		t.Fatalf("indexed %d want ≥ %d", count, n)
	}
	if st.NextUID["INBOX"] < uint32(n) {
		t.Fatalf("nextUid=%d", st.NextUID["INBOX"])
	}
}
