//go:build integration

package messages

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/index"
	"github.com/radiobros/emailbridge/internal/mailbox/ondemand"
	"github.com/radiobros/emailbridge/internal/mailproto/imapc"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/submit"
	ebsync "github.com/radiobros/emailbridge/internal/sync"
	"github.com/radiobros/emailbridge/internal/tenancy"
	"github.com/radiobros/emailbridge/simulator/simtest"
)

type fakeAccounts struct {
	acc *ebsync.Account
}

func (f *fakeAccounts) Load(_ context.Context, id string) (*ebsync.Account, error) {
	if f.acc == nil || f.acc.ID != id {
		return nil, errs.New(errs.CodeNotFound, "account missing")
	}
	cp := *f.acc
	return &cp, nil
}

type fakeSubmit struct {
	lastReq submit.SubmitRequest
	lastKey string
	calls   int
	res     *submit.SubmitResult
	err     error
}

func (f *fakeSubmit) Submit(_ context.Context, _ string, req submit.SubmitRequest, key string, _ bool) (*submit.SubmitResult, error) {
	f.calls++
	f.lastReq, f.lastKey = req, key
	if f.err != nil {
		return nil, f.err
	}
	if f.res != nil {
		return f.res, nil
	}
	return &submit.SubmitResult{
		Status:   http.StatusAccepted,
		Accepted: &submit.Accepted{ID: store.NewID(store.PrefixOutbound), State: "queued"},
	}, nil
}

func testDB(t *testing.T) *store.DB {
	t.Helper()
	uri := os.Getenv("EB_TEST_MONGO_URI")
	if uri == "" {
		uri = "mongodb://127.0.0.1:27017/?replicaSet=rs0&directConnection=true"
	}
	ctx := context.Background()
	db, err := store.Connect(ctx, store.Options{
		URI: uri, Database: fmt.Sprintf("ebtest_messages_%d", time.Now().UnixNano()),
		ConnectTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Skipf("no MongoDB: %v", err)
	}
	t.Cleanup(func() { _ = db.DB.Drop(ctx); _ = db.Close(ctx) })
	_ = Migration(24).Up(ctx, db)
	return db
}

type harness struct {
	svc  *Service
	pool *ondemand.Pool
	db   *store.DB
	acc  *ebsync.Account
	org  string
	ctx  context.Context
	sim  *simtest.Env
	user string
	sub  *fakeSubmit
}

func (h *harness) ownerCtx() context.Context {
	return tenancy.WithCaller(context.Background(), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: store.NewID(store.PrefixUser),
		OrganizationID: h.org, Role: tenancy.RoleOwner,
	})
}

func (h *harness) boundKey(accountID string) context.Context {
	return tenancy.WithCaller(context.Background(), tenancy.Caller{
		Kind: tenancy.CallerAPIKey, APIKeyID: store.NewID(store.PrefixAPIKey),
		OrganizationID: h.org, Scopes: []string{"messages:read", "messages:write", "admin"},
		AccountID: accountID,
	})
}

func rawMsg(subj, mid, body string) []byte {
	return []byte(fmt.Sprintf(
		"From: Mario <mario@example.test>\r\nTo: Team <team@example.test>\r\nSubject: %s\r\nMessage-ID: %s\r\nDate: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n%s\r\n",
		subj, mid, time.Now().UTC().Format(time.RFC1123Z), body,
	))
}

func multipartMsg(subj, mid string) []byte {
	return []byte(fmt.Sprintf(
		"From: Mario <mario@example.test>\r\nTo: Team <team@example.test>\r\nSubject: %s\r\nMessage-ID: %s\r\nDate: %s\r\nMIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=b1\r\n\r\n--b1\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nhello body\r\n--b1\r\nContent-Type: application/pdf; name=a.pdf\r\nContent-Disposition: attachment; filename=a.pdf\r\nContent-Transfer-Encoding: base64\r\n\r\nJVBERi0=\r\n--b1--\r\n",
		subj, mid, time.Now().UTC().Format(time.RFC1123Z),
	))
}

func ensureMailbox(t *testing.T, h *harness, path, special string, uv uint32) string {
	t.Helper()
	var existing index.Mailbox
	err := h.db.Collection(index.Mailboxes).FindOne(h.ctx, bson.M{
		"accountId": h.acc.ID, "path": path, "organizationId": h.org, "status": "active",
	}).Decode(&existing)
	if err == nil {
		return existing.ID
	}
	return seedMailbox(t, h, path, special, uv)
}

func seedIndex(t *testing.T, h *harness, path string, uid, uv uint32, mid string, flags []string, size int64) (mbxID, msgID string) {
	t.Helper()
	mbxID = ensureMailbox(t, h, path, "", uv)
	msgID = store.NewID(store.PrefixMessage)
	now := time.Now().UTC()
	_, err := h.db.Collection(index.Messages).InsertOne(h.ctx, &index.Message{
		ID: msgID, OrganizationID: h.org, AccountID: h.acc.ID, MailboxID: mbxID,
		UIDValidity: uv, UID: uid, Flags: flags, MessageIDHeader: mid,
		EmailID: index.EmailID(mid, now, size), InternalDate: now, Size: size, IndexedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return mbxID, msgID
}

func seedMailbox(t *testing.T, h *harness, path, special string, uv uint32) string {
	t.Helper()
	id := store.NewID(store.PrefixMailbox)
	_, err := h.db.Collection(index.Mailboxes).InsertOne(h.ctx, &index.Mailbox{
		ID: id, OrganizationID: h.org, AccountID: h.acc.ID, Path: path,
		SpecialUse: special, UIDValidity: uv, Status: "active",
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func simHarness(t *testing.T) *harness {
	t.Helper()
	db := testDB(t)
	sim := simtest.Start(t)
	user := fmt.Sprintf("u%d", time.Now().UnixNano())
	ctx := context.Background()
	if err := sim.Client.CreateUser(ctx, user, "pw"); err != nil {
		t.Fatal(err)
	}
	_ = sim.Client.CreateMailbox(ctx, user, "Drafts", `\Drafts`)
	_ = sim.Client.CreateMailbox(ctx, user, "Archive")
	org := store.NewID(store.PrefixOrganization)
	accID := store.NewID(store.PrefixAccount)
	acc := &ebsync.Account{
		ID: accID, OrganizationID: org,
		IMAPAddr: sim.Addr.IMAPPlain, IMAPTLS: imapc.TLSNone,
		IMAPUser: user, IMAPPass: "pw",
	}
	// The ownership check (G39) counts the account in its organisation: store the account row too.
	if _, err := db.Collection("accounts").InsertOne(ctx, bson.M{"_id": accID, "organizationId": org}); err != nil {
		t.Fatal(err)
	}
	pool := &ondemand.Pool{Idle: time.Minute, Metrics: ondemand.NewMetrics()}
	sub := &fakeSubmit{}
	svc := &Service{
		DB: db, Pool: pool, Accounts: &fakeAccounts{acc: acc}, Submit: sub,
	}
	h := &harness{svc: svc, pool: pool, db: db, acc: acc, org: org, sim: sim, user: user, sub: sub}
	h.ctx = h.ownerCtx()
	t.Cleanup(func() { pool.Close() })
	return h
}
