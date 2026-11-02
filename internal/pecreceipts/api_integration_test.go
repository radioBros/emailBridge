//go:build integration

package pecreceipts

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/pec"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

func testDB(t *testing.T) *store.DB {
	t.Helper()
	uri := os.Getenv("EB_TEST_MONGO_URI")
	if uri == "" {
		uri = "mongodb://127.0.0.1:27017/?replicaSet=rs0&directConnection=true"
	}
	ctx := context.Background()
	db, err := store.Connect(ctx, store.Options{
		URI: uri, Database: fmt.Sprintf("ebtest_pecapi_%d", time.Now().UnixNano()), ConnectTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Skipf("no MongoDB: %v", err)
	}
	t.Cleanup(func() { _ = db.DB.Drop(ctx); _ = db.Close(ctx) })
	if err := Migration(41).Up(ctx, db); err != nil {
		t.Fatal(err)
	}
	return db
}

func ownerCtx(org string) context.Context {
	return tenancy.WithCaller(store.WithOrganization(context.Background(), org), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: store.NewID(store.PrefixUser), OrganizationID: org, Role: tenancy.RoleOwner,
	})
}

func keyCtx(org string, scopes ...string) context.Context {
	return tenancy.WithCaller(store.WithOrganization(context.Background(), org), tenancy.Caller{
		Kind: tenancy.CallerAPIKey, APIKeyID: store.NewID(store.PrefixAPIKey), OrganizationID: org, Scopes: scopes,
	})
}

type fakeRaw struct {
	body []byte
	err  error
}

func (f *fakeRaw) Open(_ context.Context, _ *Receipt) (io.ReadCloser, error) {
	if f.err != nil {
		return nil, f.err
	}
	return io.NopCloser(bytes.NewReader(f.body)), nil
}

func seedReceipt(t *testing.T, db *store.DB, org, acc string, rc *Receipt) *Receipt {
	t.Helper()
	rc.OrganizationID = org
	rc.AccountID = acc
	if rc.ID == "" {
		rc.ID = store.NewID(store.PrefixPECReceipt)
	}
	if rc.MessageID == "" {
		rc.MessageID = store.NewID(store.PrefixMessage)
	}
	if rc.CreatedAt.IsZero() {
		rc.CreatedAt = time.Now().UTC().Truncate(time.Millisecond)
	}
	if rc.At.IsZero() {
		rc.At = rc.CreatedAt
	}
	if _, err := db.Collection(Collection).InsertOne(context.Background(), rc); err != nil {
		t.Fatal(err)
	}
	return rc
}

func TestIntegrationListGetRawAndScope(t *testing.T) {
	db := testDB(t)
	org := store.NewID(store.PrefixOrganization)
	acc := store.NewID(store.PrefixAccount)
	out := store.NewID(store.PrefixOutbound)
	ctx := ownerCtx(org)

	rawBody := []byte("From: a@pec.test\r\n\r\nreceipt\r\n")
	sum := sha256.Sum256(rawBody)
	shaHex := hex.EncodeToString(sum[:])

	at := time.Date(2026, 10, 26, 10, 0, 0, 0, time.UTC)
	seedReceipt(t, db, org, acc, &Receipt{
		Type: pec.ReceiptAcceptance, TransportID: "opec.abc",
		OutboundID: &out, At: at, SHA256: shaHex, Signature: pec.Signature{Valid: true},
	})
	deliv := seedReceipt(t, db, org, acc, &Receipt{
		Type: pec.ReceiptDelivery, TransportID: "opec.def", DeliveredTo: "rcpt@pec.test",
		OutboundID: &out, At: at.Add(time.Minute), SHA256: shaHex, Signature: pec.Signature{Valid: true},
	})
	seedReceipt(t, db, org, acc, &Receipt{
		Type: pec.ReceiptDeliveryError, TransportID: "other.xyz", DeliveredTo: "bad@pec.test",
		At: at.Add(2 * time.Minute), SHA256: shaHex,
	})

	fake := &fakeRaw{body: rawBody}
	svc := &Service{
		Repo: &Repo{DB: db}, Raw: fake, Registry: features.DefaultRegistry(), Env: "test",
		Enabled: func(context.Context, string) (bool, error) { return true, nil },
	}

	page, err := svc.Repo.List(ctx, acc, ListFilter{Limit: 2, Sort: "-at", IncludeTotal: true, Page: 1})
	if err != nil || len(page.Data) != 2 || page.Total == nil || *page.Total != 3 {
		t.Fatalf("page1: %+v %v", page, err)
	}
	page2, err := svc.Repo.List(ctx, acc, ListFilter{Limit: 2, Sort: "-at", Page: 2})
	if err != nil || len(page2.Data) != 1 {
		t.Fatalf("page2: %+v %v", page2, err)
	}
	byType, err := svc.Repo.List(ctx, acc, ListFilter{Type: pec.ReceiptDelivery, Sort: "at"})
	if err != nil || len(byType.Data) != 1 || byType.Data[0].DeliveredTo != "rcpt@pec.test" {
		t.Fatalf("type: %+v %v", byType, err)
	}
	byOut, err := svc.Repo.List(ctx, acc, ListFilter{OutboundID: out, Sort: "type"})
	if err != nil || len(byOut.Data) != 2 {
		t.Fatalf("outbound: %+v %v", byOut, err)
	}
	byQ, err := svc.Repo.List(ctx, acc, ListFilter{Q: "opec."})
	if err != nil || len(byQ.Data) != 2 {
		t.Fatalf("q transport: %+v %v", byQ, err)
	}
	byRcpt, err := svc.Repo.List(ctx, acc, ListFilter{Q: "rcpt@"})
	if err != nil || len(byRcpt.Data) != 1 {
		t.Fatalf("q recipient: %+v %v", byRcpt, err)
	}
	if _, err := svc.Repo.List(ctx, acc, ListFilter{Sort: "size"}); errs.CodeOf(err) != errs.CodeSortNotAllowed {
		t.Fatalf("bad sort: %v", err)
	}
	if _, err := svc.Repo.List(ctx, acc, ListFilter{Q: strings.Repeat("x", 201)}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("q len: %v", err)
	}
	if _, err := svc.Repo.List(ctx, acc, ListFilter{Q: ".*"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Repo.List(ctx, acc, ListFilter{OutboundID: "bad"}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("bad outboundId: %v", err)
	}
	cur, err := svc.Repo.List(ctx, acc, ListFilter{Limit: 1, Sort: "-at"})
	if err != nil || cur.NextCursor == nil {
		t.Fatalf("cursor page: %+v %v", cur, err)
	}
	cur2, err := svc.Repo.List(ctx, acc, ListFilter{Limit: 10, Sort: "-at", Cursor: *cur.NextCursor})
	if err != nil || len(cur2.Data) != 2 {
		t.Fatalf("cursor next: %+v %v", cur2, err)
	}
	if _, err := svc.Repo.List(ctx, acc, ListFilter{Cursor: "!!!", Sort: "-at"}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("bad cursor: %v", err)
	}

	listRes, err := svc.list(ctx, &listIn{AccountID: acc, Limit: 10, Sort: "-at"})
	if err != nil || len(listRes.Body.Data) != 3 {
		t.Fatalf("list handler: %+v %v", listRes, err)
	}
	id := deliv.ID
	got, err := svc.get(ctx, &receiptIDIn{ReceiptID: id})
	if err != nil || got.Body.ID != id {
		t.Fatalf("get: %+v %v", got, err)
	}

	stream, err := svc.getRaw(ctx, &receiptIDIn{ReceiptID: id})
	if err != nil || stream == nil || stream.Body == nil {
		t.Fatalf("getRaw: %v", err)
	}
	w := &streamWriter{h: map[string]string{}, buf: &bytes.Buffer{}}
	writeRaw(w, id, digestHeader(shaHex), io.NopCloser(bytes.NewReader(rawBody)))
	if w.buf.String() != string(rawBody) {
		t.Fatalf("raw body %q", w.buf.String())
	}
	wantDigest := "sha-256=" + base64.StdEncoding.EncodeToString(sum[:])
	if w.h["Digest"] != wantDigest || w.h["Content-Type"] != "message/rfc822" {
		t.Fatalf("headers %+v", w.h)
	}

	evOnly := keyCtx(org, "events:read")
	if _, err := svc.list(evOnly, &listIn{AccountID: acc}); errs.CodeOf(err) != errs.CodeScopeMissing {
		t.Fatalf("events-only list: %v", err)
	}
	if _, err := svc.get(evOnly, &receiptIDIn{ReceiptID: id}); errs.CodeOf(err) != errs.CodeScopeMissing {
		t.Fatalf("events-only get: %v", err)
	}

	svc.Enabled = func(context.Context, string) (bool, error) { return false, nil }
	if _, err := svc.list(ctx, &listIn{AccountID: acc}); errs.CodeOf(err) != errs.CodeFeatureDisabled {
		t.Fatalf("feature off: %v", err)
	}
	if _, err := svc.get(ctx, &receiptIDIn{ReceiptID: id}); errs.CodeOf(err) != errs.CodeFeatureDisabled {
		t.Fatalf("feature off get: %v", err)
	}
	svc.Enabled = func(context.Context, string) (bool, error) { return true, nil }

	if _, err := svc.get(ctx, &receiptIDIn{ReceiptID: "rcp_notarealid00000000000000"}); err == nil || errs.CodeOf(err) != errs.CodeNotFound {
		// invalid pattern may fail IsID → not_found
		if errs.CodeOf(err) != errs.CodeNotFound {
			t.Fatalf("missing: %v", err)
		}
	}
	missing := store.NewID(store.PrefixPECReceipt)
	if _, err := svc.get(ctx, &receiptIDIn{ReceiptID: missing}); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatalf("missing id: %v", err)
	}

	// Outbound View.PEC is covered in internal/submit (ViewOf); OutboundPECStatus JSON shape is S17 §5.2.
	st := OutboundPECStatus{State: StateDelivered, AcceptedAt: &at, Deliveries: []PECDelivery{
		{Recipient: "rcpt@pec.test", State: DeliveryDelivered, At: at, ReceiptID: id},
	}}
	if st.State != StateDelivered || len(st.Deliveries) != 1 || st.AcceptedAt == nil {
		t.Fatalf("pec status: %+v", st)
	}
}

func TestIntegrationCompositeRawAndModule(t *testing.T) {
	msgCalls, evCalls := 0, 0
	src := CompositeRaw{
		Message: msgOpener(func(context.Context, string, string) (io.ReadCloser, error) {
			msgCalls++
			return io.NopCloser(strings.NewReader("from-msg")), nil
		}),
		Evidence: evOpener(func(context.Context, string) (io.ReadCloser, error) {
			evCalls++
			return io.NopCloser(strings.NewReader("from-ev")), nil
		}),
	}
	r, err := src.Open(context.Background(), &Receipt{AccountID: "a", MessageID: "m", Evidence: &Evidence{Key: "k"}})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(r)
	_ = r.Close()
	if string(b) != "from-ev" || evCalls != 1 || msgCalls != 0 {
		t.Fatalf("evidence first: %q ev=%d msg=%d", b, evCalls, msgCalls)
	}
	r2, err := src.Open(context.Background(), &Receipt{AccountID: "a", MessageID: "m"})
	if err != nil {
		t.Fatal(err)
	}
	b2, _ := io.ReadAll(r2)
	_ = r2.Close()
	if string(b2) != "from-msg" || msgCalls != 1 {
		t.Fatalf("message fallback: %q msg=%d", b2, msgCalls)
	}
	if _, err := (CompositeRaw{}).Open(context.Background(), &Receipt{}); errs.CodeOf(err) != errs.CodeInternalError {
		t.Fatalf("no source: %v", err)
	}

	db := testDB(t)
	org := store.NewID(store.PrefixOrganization)
	acc := store.NewID(store.PrefixAccount)
	ctx := ownerCtx(org)
	_, _ = db.Collection("accounts").InsertOne(ctx, map[string]any{
		"_id": acc, "organizationId": org, "switches": map[string]any{"pec.enabled": true},
	})
	svc := &Service{Repo: &Repo{DB: db}, Registry: features.DefaultRegistry(), Env: "test"}
	ok, err := svc.pecEnabled(ctx, acc)
	if err != nil || !ok {
		t.Fatalf("enabled via account switch: %v %v", ok, err)
	}
	accOff := store.NewID(store.PrefixAccount)
	_, _ = db.Collection("accounts").InsertOne(ctx, map[string]any{"_id": accOff, "organizationId": org})
	ok, err = svc.pecEnabled(ctx, accOff)
	if err != nil || ok {
		t.Fatalf("default off: %v %v", ok, err)
	}

	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := app.New(app.Options{Roles: app.RoleSet{app.RoleAPI: true}, NodeID: "n", Log: quiet})
	app.Provide(a, db)
	srv, err := httpapi.New(httpapi.Options{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	app.Provide(a, srv)
	m := &Module{
		Raw: &fakeRaw{body: []byte("x")}, Env: "test", Registry: features.DefaultRegistry(),
		Enabled: func(context.Context, string) (bool, error) { return true, nil }, Log: quiet,
	}
	if err := m.Start(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if m.Service() == nil || m.Name() != "pecreceipts" || len(m.Roles()) != 1 {
		t.Fatal("module meta")
	}
	_ = m.Stop(context.Background())
}

type msgOpener func(ctx context.Context, accountID, messageID string) (io.ReadCloser, error)

func (f msgOpener) Open(ctx context.Context, accountID, messageID string) (io.ReadCloser, error) {
	return f(ctx, accountID, messageID)
}

type evOpener func(ctx context.Context, key string) (io.ReadCloser, error)

func (f evOpener) Open(ctx context.Context, key string) (io.ReadCloser, error) { return f(ctx, key) }
