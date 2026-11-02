//go:build integration

package sync

import (
	"context"
	"crypto/x509"
	"encoding/json"
	gosync "sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/events"
	"github.com/radiobros/emailbridge/internal/events/types"
	"github.com/radiobros/emailbridge/internal/evidence"
	"github.com/radiobros/emailbridge/internal/mailproto/imapc"
	"github.com/radiobros/emailbridge/internal/pec"
	"github.com/radiobros/emailbridge/internal/pecreceipts"
	"github.com/radiobros/emailbridge/internal/store"
)

// PEC receipts (S17 §4–§6) through the real sync engine: rows, pec.receipt, linking to the outbound message and
// its PEC state; a receipt delivered twice is stored and reported once.
func TestIntegrationPECReceipts(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	if err := pecreceipts.EnsureIndexes(ctx, e.db); err != nil {
		t.Fatal(err)
	}
	ca, _ := pec.NewCA("sync test CA")
	signer, _ := ca.Issue("Gestore Test", "posta-certificata@pec.sim.test", time.Time{}, time.Time{})
	anchors := x509.NewCertPool()
	anchors.AddCert(ca.Cert)

	e.accs.acc.PEC = true
	acc := e.accs.acc
	out := store.NewID(store.PrefixOutbound)
	if _, err := e.db.Collection("outboundMessages").InsertOne(ctx, bson.M{"_id": out, "organizationId": acc.OrganizationID,
		"accountId": acc.ID, "messageIdHeader": "<verbale-42@legacyapp.test>"}); err != nil {
		t.Fatal(err)
	}
	m := e.manager()
	repo := &pecreceipts.Repo{DB: e.db}
	m.PEC = &PEC{Receipts: repo, Anchors: anchors}
	rctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); m.Run(rctx) }()
	defer func() { cancel(); <-done }()
	waitFor(t, 30*time.Second, "not initialised", func() bool { return e.count(events.AccountInitialized) == 1 })

	c, err := imapc.Dial(ctx, e.imap)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	base := pec.Build{Kind: pec.KindReceipt, From: "posta-certificata@pec.sim.test", To: "sender@pec.sim.test",
		Sender: "sender@pec.sim.test", Recipients: []pec.Recipient{{Address: "rcpt@pec.sim.test", Kind: "certificato"}},
		Subject: "Verbale 42", Provider: "Gestore Test", TransportID: "opec.1@pec.sim.test", OriginalID: "<verbale-42@legacyapp.test>",
		At: time.Now().Add(-time.Minute).Truncate(time.Second)}
	accept := base
	accept.Type, accept.MessageID = pec.ReceiptAcceptance, "<acc-1@pec.sim.test>"
	deliver := base
	deliver.Type, deliver.ReceiptKind, deliver.DeliveredTo, deliver.MessageID = pec.ReceiptDelivery, "breve", "rcpt@pec.sim.test", "<dlv-1@pec.sim.test>"
	for _, b := range []pec.Build{accept, deliver, deliver} {
		raw, err := b.Generate(signer)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.Append(ctx, "INBOX", nil, time.Now(), raw); err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, 20*time.Second, "pec.receipt missing", func() bool {
		return e.count(events.PECReceipt) >= 2 && e.count(events.MessageReceived) == 3
	})
	time.Sleep(500 * time.Millisecond) // a third pec.receipt would arrive with the third message.received
	if n := e.count(events.PECReceipt); n != 2 {
		t.Fatalf("pec.receipt events = %d, want 2 (the duplicate delivery receipt is not reported)", n)
	}
	n, _ := e.db.Collection(pecreceipts.Collection).CountDocuments(ctx, bson.M{"organizationId": acc.OrganizationID})
	if n != 2 {
		t.Fatalf("receipt rows = %d", n)
	}
	var ev types.PECReceipt
	for _, raw := range e.eventsOf(events.PECReceipt) {
		_ = json.Unmarshal([]byte(raw["data"].(string)), &ev)
		if ev.Type == pec.ReceiptDelivery {
			break
		}
	}
	if ev.OutboundID != out || ev.DeliveredTo != "rcpt@pec.sim.test" || ev.TransportID != "opec.1@pec.sim.test" ||
		!ev.Signature.Valid || ev.Signature.Trusted == nil || !*ev.Signature.Trusted || len(ev.SHA256) != 64 || ev.ReceiptKind != "breve" {
		t.Fatalf("pec.receipt = %+v", ev)
	}
	var rec types.MessageReceived
	_ = json.Unmarshal([]byte(e.eventsOf(events.MessageReceived)[0]["data"].(string)), &rec)
	if rec.PEC == nil || rec.PEC.Kind != "receipt" || rec.PEC.ReceiptID == "" || !rec.PEC.Signature.Valid {
		t.Fatalf("message.received pec = %+v", rec.PEC)
	}
	var ob struct {
		PEC pecreceipts.OutboundPECStatus `bson:"pec"`
	}
	if err := e.db.Collection("outboundMessages").FindOne(ctx, bson.M{"_id": out}).Decode(&ob); err != nil {
		t.Fatal(err)
	}
	if ob.PEC.State != pecreceipts.StateDelivered || ob.PEC.AcceptedAt == nil || len(ob.PEC.Deliveries) != 1 {
		t.Fatalf("outbound pec = %+v", ob.PEC)
	}
}

type fakeEvidence struct {
	mu    gosync.Mutex
	fail  int // fail this many Archive calls first (transient storage error)
	items []evidence.Item
}

func (f *fakeEvidence) Ready() bool { return true }

func (f *fakeEvidence) Archive(_ context.Context, it evidence.Item) (evidence.Ref, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail > 0 {
		f.fail--
		return evidence.Ref{}, errs.New(errs.CodeInternalError, "s3 down")
	}
	f.items = append(f.items, it)
	until := time.Now().Add(time.Hour)
	return evidence.Ref{Key: "evidence/" + it.ID + ".eml", SHA256: "x", LockedUntil: &until}, nil
}

// With pec.evidenceArchive on, receipts and envelopes are archived before the receipt is reported; a transient
// storage failure retries the batch instead of reporting a receipt without evidence.
func TestIntegrationPECEvidenceArchive(t *testing.T) {
	e := setup(t)
	ctx := context.Background()
	if err := pecreceipts.EnsureIndexes(ctx, e.db); err != nil {
		t.Fatal(err)
	}
	ca, _ := pec.NewCA("ev CA")
	signer, _ := ca.Issue("Gestore", "posta-certificata@pec.sim.test", time.Time{}, time.Time{})
	e.accs.acc.PEC, e.accs.acc.PECEvidence = true, true
	ev := &fakeEvidence{fail: 1}
	m := e.manager()
	m.PEC = &PEC{Receipts: &pecreceipts.Repo{DB: e.db}, Evidence: ev}
	rctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); m.Run(rctx) }()
	defer func() { cancel(); <-done }()
	waitFor(t, 30*time.Second, "not initialised", func() bool { return e.count(events.AccountInitialized) == 1 })

	c, err := imapc.Dial(ctx, e.imap)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	for _, b := range []pec.Build{
		{Kind: pec.KindReceipt, Type: pec.ReceiptAcceptance, From: "posta-certificata@pec.sim.test", To: "s@pec.sim.test",
			Sender: "s@pec.sim.test", Subject: "x", TransportID: "ev-1", OriginalID: "<o-ev@x>", MessageID: "<a-ev@x>"},
		{Kind: pec.KindEnvelope, Type: "posta-certificata", From: "posta-certificata@pec.sim.test", To: "s@pec.sim.test",
			Sender: "other@pec.sim.test", Subject: "in", TransportID: "ev-2", OriginalID: "<o2-ev@x>", MessageID: "<e-ev@x>",
			Original: []byte("From: other@pec.sim.test\r\nSubject: in\r\n\r\nhello\r\n")},
	} {
		raw, err := b.Generate(signer)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.Append(ctx, "INBOX", nil, time.Now(), raw); err != nil {
			t.Fatal(err)
		}
	}
	waitFor(t, 30*time.Second, "pec.receipt missing", func() bool { return e.count(events.PECReceipt) == 1 })
	ev.mu.Lock()
	n := len(ev.items)
	ev.mu.Unlock()
	if n != 2 {
		t.Fatalf("archived %d items, want the receipt and the envelope", n)
	}
	var rc pecreceipts.Receipt
	if err := e.db.Collection(pecreceipts.Collection).FindOne(ctx, bson.M{"transportId": "ev-1"}).Decode(&rc); err != nil {
		t.Fatal(err)
	}
	if rc.Evidence == nil || rc.Evidence.Key == "" || rc.Evidence.LockedUntil == nil {
		t.Fatalf("receipt evidence ref: %+v", rc.Evidence)
	}
}
