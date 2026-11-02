package gmail

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/events"
	"github.com/radiobros/emailbridge/internal/index"
	"github.com/radiobros/emailbridge/internal/store"
	simgmail "github.com/radiobros/emailbridge/simulator/gmail"
)

func testMongo(t *testing.T) *store.DB {
	t.Helper()
	uri := os.Getenv("EB_TEST_MONGO_URI")
	if uri == "" {
		uri = "mongodb://127.0.0.1:27017/?replicaSet=rs0&directConnection=true"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	db, err := store.Connect(ctx, store.Options{URI: uri, Database: fmt.Sprintf("eb_gmail_%d", time.Now().UnixNano()), ConnectTimeout: 4 * time.Second})
	if err != nil {
		t.Skipf("mongo unavailable: %v", err)
	}
	t.Cleanup(func() {
		_ = db.DB.Drop(context.Background())
		_ = db.Close(context.Background())
	})
	return db
}

func TestStateRepo_roundTrip(t *testing.T) {
	db := testMongo(t)
	repo := &StateRepo{DB: db}
	acc := &Account{ID: store.NewID(store.PrefixAccount), OrganizationID: store.NewID(store.PrefixOrganization)}
	st, err := repo.Load(context.Background(), acc)
	if err != nil || st.AccountID != acc.ID {
		t.Fatalf("%v %+v", err, st)
	}
	st.HistoryID = "42"
	st.NextUID["INBOX"] = 7
	if err := repo.Save(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	st2, err := repo.Load(context.Background(), acc)
	if err != nil || st2.HistoryID != "42" || st2.NextUID["INBOX"] != 7 {
		t.Fatalf("%v %+v", err, st2)
	}
	if err := Migration(34).Up(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	r := &Runner{Repo: repo, State: nil, Client: &Client{}, Tokens: tok{"x", "y"}}
	loaded, err := r.load(context.Background(), acc)
	if err != nil || loaded.HistoryID != "42" {
		t.Fatalf("runner.load via repo %v %+v", err, loaded)
	}
	loaded.HistoryID = "43"
	if err := r.save(context.Background(), loaded); err != nil {
		t.Fatal(err)
	}
}

func TestModule_startAPI(t *testing.T) {
	// Sync-only start already covered; exercise Push() after API start is optional without httpapi.
	m := &Module{CallbackURL: "http://cb/", OIDCToken: "t"}
	_ = m.Push() // nil before Start
}

func TestRunner_commitWithIndex(t *testing.T) {
	db := testMongo(t)
	sim := simgmail.New()
	defer sim.Close()
	sim.SetToken("i@ex.com", "tok")
	ix := &index.Index{DB: db}
	acc := &Account{ID: store.NewID(store.PrefixAccount), OrganizationID: store.NewID(store.PrefixOrganization),
		Email: "i@ex.com", Paths: []string{"*"}}
	_, _ = db.Collection("accounts").InsertOne(context.Background(), map[string]any{
		"_id": acc.ID, "organizationId": acc.OrganizationID, "owner": map[string]any{"epoch": int64(1)},
	})
	var wrote bool
	r := &Runner{
		Client: &Client{BaseURL: sim.URL()}, Tokens: tok{"tok", "i@ex.com"}, Repo: &StateRepo{DB: db},
		Index: ix, Epoch: 1,
		Write: func(ctx context.Context, drafts []events.Draft, fn func(context.Context) error) error {
			wrote = true
			return ix.Fenced(ctx, acc.ID, 1, fn)
		},
		Draft: func(typ, subject string, data any) events.Draft {
			return events.Draft{Type: typ, OrganizationID: acc.OrganizationID, AccountID: acc.ID, Subject: subject, Data: data}
		},
	}
	if err := r.SyncOnce(context.Background(), acc); err != nil {
		t.Fatal(err)
	}
	sim.PutMessage("i@ex.com", []byte("From: a@ex.com\r\nTo: b@ex.com\r\nSubject: Ix\r\nMessage-ID: <i@e>\r\n\r\nx"),
		[]string{"INBOX", "UNREAD"})
	if err := r.SyncOnce(context.Background(), acc); err != nil {
		t.Fatal(err)
	}
	if !wrote {
		t.Fatal("expected Write")
	}
	n, _ := db.Collection(index.Messages).CountDocuments(context.Background(), map[string]any{"accountId": acc.ID})
	if n < 1 {
		t.Fatalf("index messages %d", n)
	}

	// Label update + delete via providerId index lookups (batched $in).
	id, _, _ := sim.PutMessage("i@ex.com", []byte("From: a@ex.com\r\nTo: b@ex.com\r\nSubject: Up\r\nMessage-ID: <u@e>\r\n\r\nx"),
		[]string{"INBOX", "UNREAD"})
	if err := r.SyncOnce(context.Background(), acc); err != nil {
		t.Fatal(err)
	}
	sim.ModifyLabels("i@ex.com", id, []string{"STARRED"}, []string{"UNREAD"})
	if err := r.SyncOnce(context.Background(), acc); err != nil {
		t.Fatal(err)
	}
	sim.DeleteMessage("i@ex.com", id)
	if err := r.SyncOnce(context.Background(), acc); err != nil {
		t.Fatal(err)
	}
}
