//go:build integration

package sysmail

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/mailproto/smtpc"
	"github.com/radiobros/emailbridge/internal/store"
)

func recordsDB(t *testing.T) *store.DB {
	t.Helper()
	uri := os.Getenv("EB_TEST_MONGO_URI")
	if uri == "" {
		uri = "mongodb://127.0.0.1:27017/?replicaSet=rs0&directConnection=true"
	}
	ctx := context.Background()
	db, err := store.Connect(ctx, store.Options{
		URI: uri, Database: fmt.Sprintf("ebtest_sysmail_%d", time.Now().UnixNano()), ConnectTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Skipf("no MongoDB: %v", err)
	}
	t.Cleanup(func() { _ = db.DB.Drop(ctx); _ = db.Close(ctx) })
	if err := EnsureRecordIndexes(ctx, db); err != nil {
		t.Fatal(err)
	}
	return db
}

func recordState(t *testing.T, db *store.DB, id string) Record {
	t.Helper()
	var r Record
	if err := db.Collection(RecordsCollection).FindOne(context.Background(), bson.M{"_id": id}).Decode(&r); err != nil {
		t.Fatalf("record %s: %v", id, err)
	}
	return r
}

type failPub struct{}

func (failPub) Publish(context.Context, string, []byte, string, map[string]string) (bool, error) {
	return false, errors.New("nats down")
}

func TestIntegrationRecordsLifecycle(t *testing.T) {
	db := recordsDB(t)
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	pub := &memPub{}
	s := New(Options{Mode: ModeSMTP, From: "EmailBridge <noreply@example.com>", Env: "production", Log: log}, pub)
	s.Records = &Records{DB: db}
	var sends atomic.Int32
	var fail atomic.Bool
	s.send = func(context.Context, smtpc.Options, string, []string, []byte) error {
		if fail.Load() {
			return errs.New(errs.CodeSMTPTemporary, "451 try later")
		}
		sends.Add(1)
		return nil
	}
	mail := Mail{Kind: KindInvitation, To: "test@example.com", Locale: "it", Data: map[string]string{
		"InviteURL": "https://eb.example.com/i/secret-token", "InviterName": "Ada", "OrgName": "Acme",
	}}

	// Enqueue stores the record (without the data) and publishes.
	if err := s.Enqueue(ctx, mail); err != nil {
		t.Fatal(err)
	}
	id := pub.lastID
	r := recordState(t, db, id)
	if r.State != RecordPending || r.Kind != KindInvitation || r.To != "test@example.com" || r.ExpireAt.Sub(r.CreatedAt) != RecordTTL {
		t.Fatalf("record: %+v", r)
	}
	raw, _ := db.Collection(RecordsCollection).FindOne(ctx, bson.M{"_id": id}).Raw()
	if _, err := raw.LookupErr("data"); err == nil {
		t.Fatal("mail data stored in the record")
	}

	// A temporary failure puts it back to pending with one attempt and naks.
	fail.Store(true)
	if err := s.HandleJob(ctx, pub.last); !errs.Retryable(err) {
		t.Fatalf("want retryable, got %v", err)
	}
	if r := recordState(t, db, id); r.State != RecordPending || r.Attempts != 1 {
		t.Fatalf("after retry: %+v", r)
	}
	// Success marks it sent; a redelivery of the same message is acked without a second send.
	fail.Store(false)
	if err := s.HandleJob(ctx, pub.last); err != nil {
		t.Fatal(err)
	}
	if r := recordState(t, db, id); r.State != RecordSent || sends.Load() != 1 {
		t.Fatalf("sent: %+v sends=%d", r, sends.Load())
	}
	if err := s.HandleJob(ctx, pub.last); err != nil || sends.Load() != 1 {
		t.Fatalf("redelivery sent again: %v %d", err, sends.Load())
	}

	// A cancelled record is acked without sending (the queue page cancel, W72).
	if err := s.Enqueue(ctx, mail); err != nil {
		t.Fatal(err)
	}
	cancelID := pub.lastID
	res, err := db.Collection(RecordsCollection).UpdateOne(ctx,
		bson.M{"_id": cancelID, "state": bson.M{"$in": CancellableStates}}, CancelUpdate(time.Now()))
	if err != nil || res.ModifiedCount != 1 {
		t.Fatalf("cancel: %v %+v", err, res)
	}
	if err := s.HandleJob(ctx, pub.last); err != nil || sends.Load() != 1 {
		t.Fatalf("cancelled mail sent: %v %d", err, sends.Load())
	}
	if r := recordState(t, db, cancelID); r.State != RecordCancelled {
		t.Fatalf("cancelled: %+v", r)
	}

	// A message without a record (queued before W72) still sends.
	legacy := []byte(`{"id":"job_01m5f4c3e0x2z4v6b8n1m3q5wr","kind":"alert","to":"test@example.com","locale":"en","data":{"Title":"t","Body":"b"}}`)
	if err := s.HandleJob(ctx, legacy); err != nil || sends.Load() != 2 {
		t.Fatalf("legacy: %v %d", err, sends.Load())
	}

	// Permanent failures and policy drops are recorded.
	s.send = func(context.Context, smtpc.Options, string, []string, []byte) error {
		return errs.New(errs.CodeSMTPPermanent, "550 no")
	}
	if err := s.Enqueue(ctx, mail); err != nil {
		t.Fatal(err)
	}
	if err := s.HandleJob(ctx, pub.last); err != nil {
		t.Fatal(err)
	}
	if r := recordState(t, db, pub.lastID); r.State != RecordFailed {
		t.Fatalf("permanent: %+v", r)
	}
	dev := New(Options{Mode: ModeSMTP, Env: "development", AllowList: []string{"someone@example.org"}, Log: log}, pub)
	dev.Records = s.Records
	if err := dev.Enqueue(ctx, mail); err != nil {
		t.Fatal(err)
	}
	if err := dev.HandleJob(ctx, pub.last); err != nil {
		t.Fatal(err)
	}
	if r := recordState(t, db, pub.lastID); r.State != RecordDropped {
		t.Fatalf("policy drop: %+v", r)
	}

	// A publish failure marks the record failed.
	broken := New(Options{Mode: ModeSMTP, Env: "production", Log: log}, failPub{})
	broken.Records = s.Records
	if err := broken.Enqueue(ctx, mail); err == nil {
		t.Fatal("publish failure not returned")
	}
	if n, _ := db.Collection(RecordsCollection).CountDocuments(ctx, bson.M{"state": RecordFailed}); n != 2 {
		t.Fatalf("failed records: %d", n)
	}
}

func TestIntegrationRecordsDatabaseDownFailsOpen(t *testing.T) {
	db := recordsDB(t)
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	pub := &memPub{}
	s := New(Options{Mode: ModeSMTP, Env: "production", Log: log}, pub)
	var sends atomic.Int32
	s.send = func(context.Context, smtpc.Options, string, []string, []byte) error { sends.Add(1); return nil }
	s.Records = &Records{DB: db}
	if err := s.Enqueue(ctx, Mail{Kind: KindAlert, To: "test@example.com", Data: map[string]string{"Title": "t", "Body": "b"}}); err != nil {
		t.Fatal(err)
	}
	_ = db.Close(ctx) // the database goes away: the mail is still sent, untracked
	if err := s.HandleJob(ctx, pub.last); err != nil || sends.Load() != 1 {
		t.Fatalf("fail open: %v %d", err, sends.Load())
	}
	if err := s.Enqueue(ctx, Mail{Kind: KindAlert, To: "test@example.com", Data: map[string]string{"Title": "t", "Body": "b"}}); err != nil {
		t.Fatalf("enqueue without database: %v", err)
	}
}

func TestIntegrationModuleWiresRecords(t *testing.T) {
	db := recordsDB(t)
	a := app.New(app.Options{Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	app.Provide(a, db)
	m := &Module{Options: Options{Mode: ModeOff}}
	if err := m.Start(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if m.svc.Records == nil || m.svc.Records.DB != db {
		t.Fatal("records not wired")
	}
	r := &Records{DB: db, Now: func() time.Time { return time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC) }}
	if !r.now().Equal(time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC)) {
		t.Fatal("injected clock")
	}
}
