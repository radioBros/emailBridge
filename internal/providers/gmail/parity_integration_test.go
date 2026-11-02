//go:build integration

package gmail_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/events"
	"github.com/radiobros/emailbridge/internal/events/types"
	"github.com/radiobros/emailbridge/internal/index"
	gmail "github.com/radiobros/emailbridge/internal/providers/gmail"
	"github.com/radiobros/emailbridge/internal/store"
	ebsync "github.com/radiobros/emailbridge/internal/sync"
	simgmail "github.com/radiobros/emailbridge/simulator/gmail"
)

func TestIntegrationParity_managerGmailVsMapped(t *testing.T) {
	uri := os.Getenv("EB_TEST_MONGO_URI")
	if uri == "" {
		uri = "mongodb://127.0.0.1:27017/?replicaSet=rs0&directConnection=true"
	}
	ctx := context.Background()
	db, err := store.Connect(ctx, store.Options{URI: uri, Database: fmt.Sprintf("eb_gmail_parity_%d", time.Now().UnixNano()), ConnectTimeout: 5 * time.Second})
	if err != nil {
		t.Skipf("mongo: %v", err)
	}
	t.Cleanup(func() { _ = db.DB.Drop(ctx); _ = db.Close(ctx) })
	for _, m := range []store.Migration{index.Migration(1), events.Migration(2), gmail.Migration(34)} {
		if err := m.Up(ctx, db); err != nil {
			t.Fatal(err)
		}
	}
	b, err := bus.Open(ctx, bus.Options{Mode: bus.ModeEmbedded, NodeID: "n1", DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)

	sim := simgmail.New()
	defer sim.Close()
	sim.SetToken("g@ex.com", "tok")

	org, accID := store.NewID(store.PrefixOrganization), store.NewID(store.PrefixAccount)
	if _, err := db.Collection(index.Accounts).InsertOne(ctx, bson.M{"_id": accID, "organizationId": org, "state": ebsync.StateNew, "provider": "gmail"}); err != nil {
		t.Fatal(err)
	}

	raw := []byte("From: Alice <alice@ex.com>\r\nTo: Bob <bob@ex.com>\r\nSubject: Hello\r\n" +
		"Message-ID: <same@ex.com>\r\nDate: Mon, 26 Oct 2026 12:00:00 +0000\r\n\r\nHi there\r\n")

	accs := &parityAccounts{db: db, acc: ebsync.Account{
		ID: accID, OrganizationID: org, Email: "g@ex.com", State: ebsync.StateNew,
		Provider: ebsync.ProviderGmail, Paths: []string{"*"}, NotifyFrom: time.Now().Add(-time.Hour),
		Token: func(context.Context) (string, string, error) { return "tok", "g@ex.com", nil },
	}}
	backend := &gmail.Backend{
		Client: &gmail.Client{BaseURL: sim.URL()},
		Repo:   &gmail.StateRepo{DB: db},
		Config: gmail.Config{PullInterval: 200 * time.Millisecond},
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	m := &ebsync.Manager{
		NodeID: "n1", Bus: b, Accounts: accs, Index: &index.Index{DB: db},
		Events:   &events.Writer{DB: db},
		Backends: map[string]ebsync.Backend{ebsync.ProviderGmail: backend},
		Config:   ebsync.Config{ResyncDelay: time.Hour, ReconnectMin: 100 * time.Millisecond, ReconnectMax: time.Second},
		LeaseTTL: 3 * time.Second, Tick: 150 * time.Millisecond,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	rctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); m.Run(rctx) }()
	defer func() { cancel(); <-done }()

	waitFor(t, 20*time.Second, "gmail connected", func() bool {
		var doc bson.M
		_ = db.Collection(index.Accounts).FindOne(ctx, bson.M{"_id": accID}).Decode(&doc)
		st, _ := doc["state"].(string)
		return st == ebsync.StateConnected || st == ebsync.StateSyncing
	})

	sim.PutMessage("g@ex.com", raw, []string{"INBOX", "UNREAD"})
	waitFor(t, 20*time.Second, "message.received", func() bool {
		n, _ := db.Collection(events.Collection).CountDocuments(ctx, bson.M{"type": events.MessageReceived})
		return n >= 1
	})

	cur, err := db.Collection(events.Collection).Find(ctx, bson.M{"type": events.MessageReceived})
	if err != nil {
		t.Fatal(err)
	}
	var docs []bson.M
	_ = cur.All(ctx, &docs)
	if len(docs) < 1 {
		t.Fatal("no received")
	}
	dataStr, _ := docs[0]["data"].(string)
	var got types.MessageReceived
	if err := json.Unmarshal([]byte(dataStr), &got); err != nil {
		t.Fatal(err)
	}

	// IMAP-shaped baseline: same mapper without Gmail ids (parity except provider IDs).
	imapAcc := &gmail.Account{ID: store.NewID(store.PrefixAccount), OrganizationID: org}
	st := &gmail.SyncState{UIDValidity: 1, NextUID: map[string]uint32{}, MailboxIDs: map[string]string{}}
	internal := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	imapLike, err := gmail.MapMessage(imapAcc, st, "INBOX", `\Inbox`, &gmail.Message{
		ID: "imap-synth", ThreadID: "imap-thr", LabelIDs: []string{"INBOX"},
		InternalDate: internal, Size: int64(len(raw)), Raw: raw,
	}, gmail.Needs{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	strip := func(d *types.MessageReceived) map[string]any {
		return map[string]any{
			"subject": d.Subject, "from": d.From, "to": d.To, "cc": d.Cc,
			"messageIdHeader": d.MessageIDHeader, "path": d.Mailbox.Path, "specialUse": d.Mailbox.SpecialUse,
			"size": d.Size,
		}
	}
	// Align internal date from Gmail (ms) vs header date for size/subject compare only.
	xb, _ := json.Marshal(strip(imapLike))
	yb, _ := json.Marshal(strip(&got))
	if string(xb) != string(yb) {
		t.Fatalf("parity:\n%s\n%s", xb, yb)
	}
	if got.EmailID == "" || got.ThreadID == "" || got.EmailID == "imap-synth" {
		t.Fatalf("provider ids missing: %+v", got)
	}
}

type parityAccounts struct {
	db  *store.DB
	acc ebsync.Account
}

func (a *parityAccounts) Placeable(context.Context) ([]string, error) { return []string{a.acc.ID}, nil }
func (a *parityAccounts) Load(context.Context, string) (*ebsync.Account, error) {
	c := a.acc
	return &c, nil
}
func (a *parityAccounts) Claimed(ctx context.Context, id, node string, epoch uint64) ([]ebsync.Command, error) {
	_, err := a.db.Collection(index.Accounts).UpdateOne(ctx, bson.M{"_id": id},
		bson.M{"$set": bson.M{"owner": bson.M{"nodeId": node, "epoch": int64(epoch), "since": time.Now()}}})
	return nil, err
}
func (a *parityAccounts) Transition(ctx context.Context, id string, epoch uint64, to string, _ *types.Reason) error {
	_, err := a.db.Collection(index.Accounts).UpdateOne(ctx, bson.M{"_id": id, "owner.epoch": int64(epoch)},
		bson.M{"$set": bson.M{"state": to}})
	return err
}

func waitFor(t *testing.T, d time.Duration, msg string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timeout: %s", msg)
}
