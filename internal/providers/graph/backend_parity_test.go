package graph_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/events"
	"github.com/radiobros/emailbridge/internal/events/types"
	"github.com/radiobros/emailbridge/internal/index"
	"github.com/radiobros/emailbridge/internal/providers/graph"
	"github.com/radiobros/emailbridge/internal/store"
	ebsync "github.com/radiobros/emailbridge/internal/sync"
	simgraph "github.com/radiobros/emailbridge/simulator/graph"
	"github.com/radiobros/emailbridge/simulator/simtest"
)

// TestBackend_parityViaManager runs a Graph account through the real sync Manager with
// Backends["graph"], then compares message.received to an IMAP-shaped payload from the same RFC822
// (and smoke-injects the same bytes into the IMAP simulator).
func TestBackend_parityViaManager(t *testing.T) {
	uri := "mongodb://127.0.0.1:27017/?replicaSet=rs0&directConnection=true"
	ctx := context.Background()
	db, err := store.Connect(ctx, store.Options{URI: uri, Database: fmt.Sprintf("eb_graph_parity_%d", time.Now().UnixNano()), ConnectTimeout: 3 * time.Second})
	if err != nil {
		t.Skipf("no MongoDB: %v", err)
	}
	t.Cleanup(func() { _ = db.DB.Drop(ctx); _ = db.Close(ctx) })
	for _, m := range []store.Migration{index.Migration(1), events.Migration(2)} {
		if err := m.Up(ctx, db); err != nil {
			t.Fatal(err)
		}
	}
	b, err := bus.Open(ctx, bus.Options{Mode: bus.ModeEmbedded, NodeID: "n1", DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)

	raw := []byte("From: Alice <alice@ex.com>\r\nTo: Bob <bob@ex.com>\r\nSubject: Parity\r\n" +
		"Message-ID: <parity@ex.com>\r\nDate: Mon, 26 Oct 2026 12:00:00 +0000\r\n\r\nHi\r\n")

	gsim := simgraph.New()
	defer gsim.Close()
	gsim.SetToken("g@ex.com", "gtok")

	gOrg, gAcc := store.NewID(store.PrefixOrganization), store.NewID(store.PrefixAccount)
	if _, err := db.Collection(index.Accounts).InsertOne(ctx, map[string]any{"_id": gAcc, "organizationId": gOrg, "state": ebsync.StateNew}); err != nil {
		t.Fatal(err)
	}
	gAccounts := &parityAccounts{db: db, acc: ebsync.Account{
		ID: gAcc, OrganizationID: gOrg, Email: "g@ex.com", Provider: ebsync.ProviderGraph,
		Paths: []string{"INBOX"}, NotifyFrom: time.Now().Add(-time.Hour),
		Token: func(context.Context) (string, string, error) { return "gtok", "g@ex.com", nil },
	}}
	gBackend := &graph.Backend{
		Client: &graph.Client{BaseURL: gsim.URL()},
		Config: graph.Config{PullInterval: 200 * time.Millisecond},
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	ix := &index.Index{DB: db}
	gm := &ebsync.Manager{
		NodeID: "n1", Bus: b, Accounts: gAccounts, Index: ix, Needs: ebsync.NoNeeds{},
		Events: &events.Writer{DB: db}, Backends: map[string]ebsync.Backend{ebsync.ProviderGraph: gBackend},
		Config:   ebsync.Config{ResyncDelay: time.Hour, ReconnectMin: 100 * time.Millisecond, ReconnectMax: time.Second},
		LeaseTTL: 5 * time.Second, Tick: 100 * time.Millisecond,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	gctx, gcancel := context.WithCancel(ctx)
	gdone := make(chan struct{})
	go func() { defer close(gdone); gm.Run(gctx) }()
	defer func() { gcancel(); <-gdone }()

	waitParity(t, 20*time.Second, "graph connected", func() bool {
		var doc map[string]any
		_ = db.Collection(index.Accounts).FindOne(ctx, map[string]any{"_id": gAcc}).Decode(&doc)
		return doc["state"] == ebsync.StateConnected
	})
	time.Sleep(500 * time.Millisecond) // baseline delta link
	gsim.PutMessage("g@ex.com", raw, "inbox")
	waitParity(t, 15*time.Second, "graph message.received", func() bool {
		n, _ := db.Collection(events.Collection).CountDocuments(ctx, map[string]any{"accountId": gAcc, "type": events.MessageReceived})
		return n >= 1
	})

	var gData types.MessageReceived
	cur, err := db.Collection(events.Collection).Find(ctx, map[string]any{"accountId": gAcc, "type": events.MessageReceived})
	if err != nil {
		t.Fatal(err)
	}
	var docs []map[string]any
	_ = cur.All(ctx, &docs)
	if len(docs) == 0 {
		t.Fatal("no graph received")
	}
	dataStr, _ := docs[0]["data"].(string)
	if dataStr == "" {
		t.Fatal("event data empty")
	}
	if err := json.Unmarshal([]byte(dataStr), &gData); err != nil {
		t.Fatal(err)
	}

	imapLike := graph.IMAPShapedReceived(graph.Account{ID: gAcc, OrganizationID: gOrg},
		gData.Mailbox.MailboxID, "INBOX", `\Inbox`, gData.UID, gData.UIDValidity, raw)
	if err := compareParity(&gData, imapLike); err != nil {
		t.Fatal(err)
	}

	sim := simtest.Start(t)
	user := fmt.Sprintf("parity%d", time.Now().UnixNano())
	if err := sim.Client.CreateUser(ctx, user, "pw"); err != nil {
		t.Fatal(err)
	}
	if _, err := sim.Client.InjectMessage(ctx, user, "INBOX", raw, nil, time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
}

func compareParity(graphEv, imapEv *types.MessageReceived) error {
	strip := func(d *types.MessageReceived) any {
		return map[string]any{
			"subject": d.Subject, "from": d.From, "to": d.To, "cc": d.Cc,
			"messageIdHeader": d.MessageIDHeader, "path": d.Mailbox.Path, "specialUse": d.Mailbox.SpecialUse,
			"inReplyTo": d.InReplyTo,
		}
	}
	xb, _ := json.Marshal(strip(graphEv))
	yb, _ := json.Marshal(strip(imapEv))
	if string(xb) != string(yb) {
		return fmt.Errorf("parity mismatch:\ngraph=%s\nimap=%s", xb, yb)
	}
	if graphEv.EmailID == "" || graphEv.ThreadID == "" {
		return fmt.Errorf("graph provider ids empty")
	}
	return nil
}

func waitParity(t *testing.T, d time.Duration, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timeout: %s", what)
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
	_, err := a.db.Collection(index.Accounts).UpdateOne(ctx,
		map[string]any{"_id": id},
		map[string]any{"$set": map[string]any{"owner": map[string]any{"nodeId": node, "epoch": int64(epoch), "since": time.Now()}}})
	return nil, err
}
func (a *parityAccounts) Transition(ctx context.Context, id string, epoch uint64, to string, _ *types.Reason) error {
	_, err := a.db.Collection(index.Accounts).UpdateOne(ctx,
		map[string]any{"_id": id, "owner.epoch": int64(epoch)},
		map[string]any{"$set": map[string]any{"state": to}})
	return err
}
