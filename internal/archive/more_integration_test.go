//go:build integration

package archive

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/index"
	"github.com/radiobros/emailbridge/internal/store"
	ebsync "github.com/radiobros/emailbridge/internal/sync"
)

func TestIntegrationArchiveAPISuccess(t *testing.T) {
	h := setup(t)
	ctx := context.Background()
	body := rawMsg("arch", "<arch@x>")
	msgID := store.NewID(store.PrefixMessage)
	key, err := h.store.PutRaw(ctx, h.org, h.accID, msgID, time.Now().UTC(), body)
	if err != nil {
		t.Fatal(err)
	}
	mailboxID := store.NewID(store.PrefixMailbox)
	_, _ = h.db.Collection(index.Mailboxes).InsertOne(ctx, bson.M{
		"_id": mailboxID, "organizationId": h.org, "accountId": h.accID, "path": "INBOX", "status": "active",
	})
	_, _ = h.db.Collection(index.Messages).InsertOne(ctx, bson.M{
		"_id": msgID, "organizationId": h.org, "accountId": h.accID, "mailboxId": mailboxID,
		"uidValidity": uint32(1), "uid": uint32(9), "archiveRef": key,
		"internalDate": time.Now().UTC(), "size": int64(len(body)), "indexedAt": time.Now().UTC(),
		"emailId": "e", "flags": []string{}, "modseq": int64(1),
	})

	quiet := slog.New(slog.DiscardHandler)
	srv, err := httpapi.New(httpapi.Options{AccessLog: quiet, SecurityLog: quiet})
	if err != nil {
		t.Fatal(err)
	}
	h.svc.Register(srv)
	req := httptest.NewRequest(http.MethodGet, "/v1/accounts/"+h.accID+"/archive/"+msgID, nil).WithContext(h.ctx)
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("%d %s", rr.Code, rr.Body.String())
	}
	if rr.Header().Get("EB-Source") != "" {
		t.Fatalf("archive-only must not set EB-Source: %q", rr.Header().Get("EB-Source"))
	}
	if !bytes.Contains(rr.Body.Bytes(), []byte("Subject: arch")) {
		t.Fatalf("%s", rr.Body.String())
	}

	res, err := h.svc.resolveArchive(h.ctx, h.accID, msgID)
	if err != nil || res.Source != SourceArchive {
		t.Fatalf("%v %+v", err, res)
	}
	_ = res.Reader.Close()
}

func TestIntegrationRawNoMailboxNoArchive(t *testing.T) {
	h := setup(t)
	msgID := h.seedIndexed(t, 99, 1, "", time.Now().UTC())
	_, err := h.svc.resolveRaw(h.ctx, h.accID, msgID)
	if errs.CodeOf(err) != errs.CodeMessageNotFound {
		t.Fatalf("%v", err)
	}
}

func TestIntegrationLoadMessageBadID(t *testing.T) {
	h := setup(t)
	_, err := h.svc.loadMessage(h.ctx, h.accID, "not-a-msg-id")
	if errs.CodeOf(err) != errs.CodeMessageNotFound {
		t.Fatalf("%v", err)
	}
}

func TestIntegrationFromArchiveMissingObject(t *testing.T) {
	h := setup(t)
	msgID := store.NewID(store.PrefixMessage)
	mailboxID := store.NewID(store.PrefixMailbox)
	_, _ = h.db.Collection(index.Mailboxes).InsertOne(context.Background(), bson.M{
		"_id": mailboxID, "organizationId": h.org, "accountId": h.accID, "path": "INBOX", "status": "active",
	})
	_, _ = h.db.Collection(index.Messages).InsertOne(context.Background(), bson.M{
		"_id": msgID, "organizationId": h.org, "accountId": h.accID, "mailboxId": mailboxID,
		"uidValidity": uint32(1), "uid": uint32(1),
		"archiveRef":   "archive/" + h.org + "/" + h.accID + "/2020/01/" + msgID + ".eml",
		"internalDate": time.Now().UTC(), "size": int64(1), "indexedAt": time.Now().UTC(),
		"emailId": "e", "flags": []string{}, "modseq": int64(1),
	})
	_, err := h.svc.resolveArchive(h.ctx, h.accID, msgID)
	if errs.CodeOf(err) != errs.CodeArchiveNotFound {
		t.Fatalf("%v", err)
	}
}

func TestIntegrationPoolFingerprintMismatchAndRun(t *testing.T) {
	h := setup(t)
	ctx := context.Background()
	l1, err := h.pool.Acquire(ctx, h.acc)
	if err != nil {
		t.Fatal(err)
	}
	l1.Release()
	s := h.pool.slotOf(h.acc.ID)
	// take() drops idle client when fingerprint no longer matches
	if c := s.take(time.Hour, "other|user"); c != nil {
		t.Fatal("mismatched fingerprint should close, not return")
	}
	rctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); h.pool.Run(rctx) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	<-done
}

func TestIntegrationModuleStartErrors(t *testing.T) {
	db := testDB(t)
	objs := testS3(t)
	quiet := slog.New(slog.DiscardHandler)

	// api without Accounts
	a := app.New(app.Options{Roles: app.RoleSet{app.RoleAPI: true}, NodeID: "n1", Log: quiet})
	app.Provide(a, db)
	app.Provide(a, objs)
	app.Provide(a, egress.NewDialer(egress.Policy{}))
	srv, _ := httpapi.New(httpapi.Options{AccessLog: quiet, SecurityLog: quiet})
	app.Provide(a, srv)
	m := &Module{Log: quiet}
	if err := m.Start(context.Background(), a); err == nil || !strings.Contains(err.Error(), "Accounts") {
		t.Fatalf("want Accounts required, got %v", err)
	}

	// non-api: no Accounts needed
	a2 := app.New(app.Options{Roles: app.RoleSet{app.RoleSync: true}, NodeID: "n2", Log: quiet})
	app.Provide(a2, db)
	app.Provide(a2, objs)
	m2 := &Module{Log: quiet}
	if err := m2.Start(context.Background(), a2); err != nil {
		t.Fatal(err)
	}
	if m2.Archiver() == nil {
		t.Fatal("archiver")
	}
	_ = m2.Stop(context.Background())
}

func TestIntegrationRetentionInvalidAndBatches(t *testing.T) {
	h := setup(t)
	ctx := context.Background()
	_, _ = h.db.Collection("accounts").InsertOne(ctx, bson.M{
		"_id": store.NewID(store.PrefixAccount), "organizationId": h.org, "archiveRetention": "nope",
	})
	h.svc.SweepRetention(ctx) // skips invalid

	// many old messages → multiple batches
	_, _ = h.db.Collection("accounts").InsertOne(ctx, bson.M{
		"_id": h.accID, "organizationId": h.org, "archiveRetention": "1h",
	})
	old := time.Now().UTC().Add(-2 * time.Hour)
	for i := 0; i < 3; i++ {
		id := store.NewID(store.PrefixMessage)
		key, err := h.store.PutRaw(ctx, h.org, h.accID, id, old, []byte("x"))
		if err != nil {
			t.Fatal(err)
		}
		_, _ = h.db.Collection(index.Messages).InsertOne(ctx, bson.M{
			"_id": id, "organizationId": h.org, "accountId": h.accID,
			"mailboxId": "mb", "uidValidity": uint32(1), "uid": uint32(i + 1),
			"archiveRef": key, "internalDate": old, "size": int64(1),
			"indexedAt": old, "emailId": "e", "flags": []string{}, "modseq": int64(1),
		})
	}
	h.svc.RetentionEvery = time.Millisecond
	rctx, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	h.svc.RetentionDuty(rctx)
}

func TestIntegrationMailboxPathMissing(t *testing.T) {
	h := setup(t)
	_, err := h.svc.mailboxPath(context.Background(), store.NewID(store.PrefixMailbox))
	if errs.CodeOf(err) != errs.CodeMessageNotFound {
		t.Fatalf("%v", err)
	}
}

func TestIntegrationReleaseClosesPriorIdle(t *testing.T) {
	h := setup(t)
	ctx := context.Background()
	l1, err := h.pool.Acquire(ctx, h.acc)
	if err != nil {
		t.Fatal(err)
	}
	l1.Release()
	s := h.pool.slotOf(h.acc.ID)
	s.mu.Lock()
	prior := s.client
	s.mu.Unlock()
	if prior == nil {
		t.Fatal("expected idle client after Release")
	}
	l2, err := h.pool.Acquire(ctx, h.acc)
	if err != nil {
		t.Fatal(err)
	}
	// inject another idle client so Release closes it
	s.mu.Lock()
	s.client = prior
	s.mu.Unlock()
	l2.Release()
	h.pool.Close()
}

func TestIntegrationFromMailboxLoadError(t *testing.T) {
	h := setup(t)
	h.svc.Accounts = fixedLoader{&ebsync.Account{ID: "other"}}
	msgID := h.seedIndexed(t, 1, 1, "", time.Now().UTC())
	_, ok := h.svc.fromMailbox(h.ctx, h.accID, &msgDoc{
		MailboxID: func() string {
			var m bson.M
			_ = h.db.Collection(index.Messages).FindOne(context.Background(), bson.M{"_id": msgID}).Decode(&m)
			return m["mailboxId"].(string)
		}(),
		UIDValidity: 1, UID: 1,
	})
	if ok {
		t.Fatal("expected false on load error")
	}
}

func TestIntegrationStoreDeleteMissing(t *testing.T) {
	h := setup(t)
	if err := h.store.Delete(context.Background(), "archive/no/such/key.eml"); err != nil {
		t.Fatal(err)
	}
}
