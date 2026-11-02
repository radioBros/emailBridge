package gmail

import (
	"context"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/events"
	"github.com/radiobros/emailbridge/internal/index"
	"github.com/radiobros/emailbridge/internal/store"
	simgmail "github.com/radiobros/emailbridge/simulator/gmail"
)

// Large mailbox: per-message state on index rows; gmailSyncState stays tiny.
func TestSyncState_largeMailboxStaysSmall(t *testing.T) {
	db := testMongo(t)
	sim := simgmail.New()
	defer sim.Close()
	sim.SetToken("big@ex.com", "tok")

	const n = 20_000
	raw := []byte("From: a@ex.com\r\nTo: b@ex.com\r\nSubject: seed\r\nMessage-ID: <seed@e>\r\n\r\nx")
	sim.SeedMessages("big@ex.com", n, raw, []string{"INBOX"})

	ix := &index.Index{DB: db}
	acc := &Account{
		ID: store.NewID(store.PrefixAccount), OrganizationID: store.NewID(store.PrefixOrganization),
		Email: "big@ex.com", Paths: []string{"INBOX"},
	}
	_, _ = db.Collection("accounts").InsertOne(context.Background(), map[string]any{
		"_id": acc.ID, "organizationId": acc.OrganizationID, "owner": map[string]any{"epoch": int64(1)},
	})
	repo := &StateRepo{DB: db}
	sink := &MemorySink{}
	r := &Runner{
		Client: &Client{BaseURL: sim.URL()}, Tokens: tok{"tok", "big@ex.com"}, Repo: repo,
		Sink: sink, Index: ix, Epoch: 1,
		Write: func(ctx context.Context, drafts []events.Draft, fn func(context.Context) error) error {
			for _, d := range drafts {
				_ = sink.Emit(ctx, acc.OrganizationID, acc.ID, d.Type, d.Subject, d.Data)
			}
			return ix.Fenced(ctx, acc.ID, 1, fn)
		},
		Draft: func(typ, subject string, data any) events.Draft {
			return events.Draft{Type: typ, OrganizationID: acc.OrganizationID, AccountID: acc.ID, Subject: subject, Data: data}
		},
		Config: Config{PullInterval: time.Hour},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := r.SyncOnce(ctx, acc); err != nil {
		t.Fatal(err)
	}
	if len(sink.ReceivedOf()) != 0 {
		t.Fatalf("baseline must be silent, got %d", len(sink.ReceivedOf()))
	}
	st, err := repo.Load(ctx, acc)
	if err != nil {
		t.Fatal(err)
	}
	rawBSON, err := bson.Marshal(st)
	if err != nil {
		t.Fatal(err)
	}
	if len(rawBSON) >= 64*1024 {
		t.Fatalf("SyncState BSON %d bytes; want < 64 KiB", len(rawBSON))
	}
	indexed, err := db.Collection(index.Messages).CountDocuments(ctx, bson.M{"accountId": acc.ID, "providerId": bson.M{"$exists": true}})
	if err != nil {
		t.Fatal(err)
	}
	if indexed < int64(n) {
		t.Fatalf("index rows with providerId: %d want ≥ %d", indexed, n)
	}

	sink.Events = nil
	sim.PutMessage("big@ex.com", []byte("From: a@ex.com\r\nTo: b@ex.com\r\nSubject: new\r\nMessage-ID: <new@e>\r\n\r\ny"),
		[]string{"INBOX", "UNREAD"})
	if err := r.SyncOnce(ctx, acc); err != nil {
		t.Fatal(err)
	}
	got := sink.ReceivedOf()
	if len(got) != 1 || got[0].Subject != "new" {
		t.Fatalf("incremental after large baseline: %+v", got)
	}
	st2, err := repo.Load(ctx, acc)
	if err != nil {
		t.Fatal(err)
	}
	raw2, err := bson.Marshal(st2)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw2) >= 64*1024 {
		t.Fatalf("SyncState after incremental %d bytes", len(raw2))
	}
}
