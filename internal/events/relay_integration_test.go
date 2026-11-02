//go:build integration

package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/events/types"
	"github.com/radiobros/emailbridge/internal/logging"
	"github.com/radiobros/emailbridge/internal/store"
)

func testDB(t *testing.T) *store.DB {
	t.Helper()
	uri := os.Getenv("EB_TEST_MONGO_URI")
	if uri == "" {
		uri = "mongodb://127.0.0.1:27017/?replicaSet=rs0&directConnection=true"
	}
	ctx := context.Background()
	db, err := store.Connect(ctx, store.Options{URI: uri, Database: fmt.Sprintf("ebtest_events_%d", time.Now().UnixNano()), ConnectTimeout: 5 * time.Second})
	if err != nil {
		t.Skipf("no MongoDB: %v", err)
	}
	t.Cleanup(func() { _ = db.DB.Drop(ctx); _ = db.Close(ctx) })
	if err := Migration(1).Up(ctx, db); err != nil {
		t.Fatal(err)
	}
	return db
}

func testBus(t *testing.T) *bus.Bus {
	t.Helper()
	b, err := bus.Open(context.Background(), bus.Options{Mode: bus.ModeEmbedded, NodeID: "t1", DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)
	return b
}

// collect consumes eb.evt.> into a slice.
func collect(t *testing.T, b *bus.Bus) func() []Event {
	t.Helper()
	var mu sync.Mutex
	var got []Event
	stop, err := b.SubscribeStream(context.Background(), bus.StreamEvents, "eb.evt.>", true, func(_ string, data []byte) {
		var e Event
		if json.Unmarshal(data, &e) == nil {
			mu.Lock()
			got = append(got, e)
			mu.Unlock()
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(stop)
	return func() []Event { mu.Lock(); defer mu.Unlock(); return append([]Event(nil), got...) }
}

func waitN(t *testing.T, get func() []Event, n int, d time.Duration) []Event {
	t.Helper()
	deadline := time.Now().Add(d)
	for {
		g := get()
		if len(g) >= n {
			return g
		}
		if time.Now().After(deadline) {
			t.Fatalf("got %d events, want %d", len(g), n)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func draftN(i int) Draft {
	d := received()
	d.Data = &types.MessageReceived{AccountRef: types.AccountRef{AccountID: testAcc}, Subject: fmt.Sprintf("m%03d", i)}
	return d
}

func TestIntegrationOutboxInTransaction(t *testing.T) {
	db := testDB(t)
	w := &Writer{DB: db, Partitions: 4}
	ctx := context.Background()
	// committed transaction: events exist
	if err := db.WithTx(ctx, func(tx context.Context) error {
		_, err := w.Emit(tx, draftN(1), draftN(2))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	// aborted transaction: no event
	boom := errors.New("state change failed")
	err := db.WithTx(ctx, func(tx context.Context) error {
		if _, err := w.Emit(tx, draftN(3)); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatal(err)
	}
	n, _ := db.Collection(Collection).CountDocuments(ctx, bson.M{})
	if n != 2 {
		t.Fatalf("stored %d events, want 2 (the aborted one must not exist)", n)
	}
	var d doc
	if err := db.Collection(Collection).FindOne(ctx, bson.M{}).Decode(&d); err != nil {
		t.Fatal(err)
	}
	if d.Published || d.Partition != bus.EventPartition(testAcc, 4) || d.ExpireAt.Sub(d.Time) != 7*24*time.Hour {
		t.Fatalf("doc %+v", d)
	}
	sctx := store.WithOrganization(ctx, testOrg)
	got, err := w.Get(sctx, d.ID)
	if err != nil || got.ID != d.ID || got.Type != MessageReceived {
		t.Fatalf("get %v %+v", err, got)
	}
	if _, err := w.Get(store.WithOrganization(ctx, store.NewID(store.PrefixOrganization)), d.ID); err == nil {
		t.Fatal("another organisation must not read the event")
	}
}

func TestIntegrationRelayOrderAndDedup(t *testing.T) {
	db := testDB(t)
	b := testBus(t)
	events := collect(t, b)
	w := &Writer{DB: db, Partitions: 4}
	ctx := context.Background()
	const total = 250 // more than one batch
	for i := 0; i < total; i += 50 {
		batch := make([]Draft, 0, 50)
		for j := i; j < i+50; j++ {
			batch = append(batch, draftN(j))
		}
		if _, err := w.Emit(ctx, batch...); err != nil {
			t.Fatal(err)
		}
	}
	r := &Relay{DB: db, Pub: b, Partitions: 4, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	p := bus.EventPartition(testAcc, 4)
	n, err := r.Drain(ctx, p)
	if err != nil || n != total {
		t.Fatalf("drained %d %v", n, err)
	}
	got := waitN(t, events, total, 5*time.Second)
	for i, e := range got {
		var data types.MessageReceived
		_ = json.Unmarshal(e.Data, &data)
		if data.Subject != fmt.Sprintf("m%03d", i) {
			t.Fatalf("event %d out of order: %s", i, data.Subject)
		}
	}
	// crash between ack and mark: mark everything unpublished again and drain → JetStream drops duplicates
	if _, err := db.Collection(Collection).UpdateMany(ctx, bson.M{}, bson.M{"$set": bson.M{"published": false}}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Drain(ctx, p); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if len(events()) != total {
		t.Fatalf("duplicates delivered: %d", len(events()))
	}
	if lag, err := r.Lag(ctx); err != nil || lag != 0 {
		t.Fatalf("lag %v %v", lag, err)
	}
}

type failingPub struct {
	inner Publisher
	after atomic.Int64
	n     atomic.Int64
}

func newFailingPub(inner Publisher, after int64) *failingPub {
	f := &failingPub{inner: inner}
	f.after.Store(after)
	return f
}

func (f *failingPub) Publish(ctx context.Context, subject string, data []byte, msgID string, h map[string]string) (bool, error) {
	if f.n.Add(1) > f.after.Load() {
		return false, errors.New("jetstream unavailable")
	}
	return f.inner.Publish(ctx, subject, data, msgID, h)
}

func TestIntegrationRelayStopsAtFirstFailure(t *testing.T) {
	db := testDB(t)
	b := testBus(t)
	w := &Writer{DB: db, Partitions: 1}
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		if _, err := w.Emit(ctx, draftN(i)); err != nil {
			t.Fatal(err)
		}
	}
	r := &Relay{DB: db, Pub: newFailingPub(b, 2), Partitions: 1}
	n, err := r.Drain(ctx, 0)
	if err == nil || n != 2 {
		t.Fatalf("published %d, err %v", n, err)
	}
	left, _ := db.Collection(Collection).CountDocuments(ctx, bson.M{"published": false})
	if left != 3 {
		t.Fatalf("unpublished %d", left)
	}
	if lag, _ := r.Lag(ctx); lag <= 0 {
		t.Fatal("lag must report the oldest unpublished event")
	}
	r.Pub = b
	if n, err := r.Drain(ctx, 0); err != nil || n != 3 {
		t.Fatalf("resume %d %v", n, err)
	}
}

func TestIntegrationModuleRelaysLive(t *testing.T) {
	db := testDB(t)
	b := testBus(t)
	events := collect(t, b)
	a := app.New(app.Options{Roles: app.RoleSet{app.RoleSync: true}, NodeID: "n1", Log: slog.New(slog.NewTextHandler(io.Discard, nil))})
	app.Provide(a, db)
	app.Provide(a, b)
	m := &Module{Partitions: 4, LeaseTTL: 3 * time.Second}
	ctx := context.Background()
	if err := m.Start(ctx, a); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Stop(ctx) }()
	m.relay.Poll = time.Minute // prove the change stream, not the poll, wakes the relay
	w, err := app.MustGet[*Writer](a)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond) // leases claimed, change stream open
	start := time.Now()
	if err := db.WithTx(ctx, func(tx context.Context) error { _, err := w.Emit(tx, draftN(1)); return err }); err != nil {
		t.Fatal(err)
	}
	waitN(t, events, 1, 10*time.Second)
	t.Logf("commit → JetStream in %v", time.Since(start))
}

func TestIntegrationWorkRetriesAfterFailure(t *testing.T) {
	db := testDB(t)
	b := testBus(t)
	events := collect(t, b)
	w := &Writer{DB: db}
	ctx := context.Background()
	if _, err := w.Emit(ctx, draftN(1), draftN(2), draftN(3)); err != nil {
		t.Fatal(err)
	}
	fp := newFailingPub(b, 1)
	r := &Relay{DB: db, Pub: fp, Poll: 50 * time.Millisecond, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	wctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); r.Work(wctx, bus.EventPartition(testAcc, 64), 1) }()
	waitN(t, events, 1, 5*time.Second)
	fp.after.Store(1 << 30) // JetStream back: the worker resumes after its backoff
	waitN(t, events, 3, 10*time.Second)
	cancel()
	<-done
	m := &Module{}
	if m.Name() != "events" || m.Roles() != nil || m.Relay() != nil || (&Relay{}).partitions() != 64 || (&Writer{}).partitions() != 64 {
		t.Fatal("module accessors")
	}
}

type headerPub struct {
	mu      sync.Mutex
	headers []map[string]string
}

func (h *headerPub) Publish(_ context.Context, _ string, _ []byte, _ string, hdr map[string]string) (bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.headers = append(h.headers, hdr)
	return false, nil
}

// The NATS message carries a traceparent that continues the event's stored trace (S12 §10), or none when the
// event has no trace context.
func TestIntegrationRelayPropagatesTraceContext(t *testing.T) {
	db := testDB(t)
	w := &Writer{DB: db, Partitions: 1}
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.NeverSample())))
	t.Cleanup(func() { otel.SetTracerProvider(prev) })

	traced, span := logging.StartSpan(context.Background(), "test", "", trace.SpanKindInternal)
	span.End()
	if _, err := w.Emit(traced, draftN(0)); err != nil {
		t.Fatal(err)
	}
	pub := &headerPub{}
	if _, err := (&Relay{DB: db, Pub: pub, Partitions: 1}).Drain(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	if len(pub.headers) != 1 {
		t.Fatalf("published %d", len(pub.headers))
	}
	tp := pub.headers[0]["traceparent"]
	if logging.TraceIDOf(tp) != span.SpanContext().TraceID().String() {
		t.Fatalf("header traceparent %q does not continue trace %s", tp, span.SpanContext().TraceID())
	}
	if tp == logging.TraceParent(traced) {
		t.Fatal("the publish must run under its own span")
	}
}
