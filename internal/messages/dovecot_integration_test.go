//go:build integration

package messages

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/mailbox/ondemand"
	"github.com/radiobros/emailbridge/internal/mailproto/imapc"
	"github.com/radiobros/emailbridge/internal/store"
	ebsync "github.com/radiobros/emailbridge/internal/sync"
	"github.com/radiobros/emailbridge/test/containers"
)

func dovecotAddrs(t *testing.T) containers.DovecotAddrs {
	t.Helper()
	if a := os.Getenv("EB_TEST_IMAP_ADDR"); a != "" {
		pass := os.Getenv("EB_TEST_IMAP_PASSWORD")
		if pass == "" {
			pass = "devpass"
		}
		return containers.DovecotAddrs{
			IMAPS: a, IMAP: os.Getenv("EB_TEST_IMAP_STARTTLS_ADDR"),
			Password: pass,
		}
	}
	// Prefer the compose dev stack (deploy/compose/devdeps.yml) when it is already listening — avoids a
	// second Dovecot under testcontainers (reaper races under parallel suites).
	if a, ok := containers.DovecotCompose(); ok {
		return a
	}
	return containers.DovecotShared(t)
}

func dovecotHarness(t *testing.T) *harness {
	t.Helper()
	db := testDB(t)
	d := dovecotAddrs(t)
	user := fmt.Sprintf("msg%d", time.Now().UnixNano())
	org := store.NewID(store.PrefixOrganization)
	accID := store.NewID(store.PrefixAccount)
	acc := &ebsync.Account{
		ID: accID, OrganizationID: org,
		IMAPAddr: d.IMAPS, IMAPTLS: imapc.TLSImplicit, IgnoreCertErrors: true,
		IMAPUser: user, IMAPPass: d.Password,
	}
	if _, err := db.Collection("accounts").InsertOne(context.Background(), bson.M{"_id": accID, "organizationId": org}); err != nil {
		t.Fatal(err)
	}
	pool := &ondemand.Pool{Idle: time.Minute, Metrics: ondemand.NewMetrics()}
	sub := &fakeSubmit{}
	svc := &Service{DB: db, Pool: pool, Accounts: &fakeAccounts{acc: acc}, Submit: sub}
	h := &harness{svc: svc, pool: pool, db: db, acc: acc, org: org, user: user, sub: sub}
	h.ctx = h.ownerCtx()
	t.Cleanup(func() { pool.Close() })
	return h
}

func TestIntegrationDovecotMailboxUploadFlagsMove(t *testing.T) {
	h := dovecotHarness(t)
	ctx := h.ctx

	created, err := h.svc.CreateMailbox(ctx, h.acc.ID, CreateMailboxInput{Path: "Work"})
	if err != nil || created.Path != "Work" {
		t.Fatalf("%+v %v", created, err)
	}

	// Probe UIDVALIDITY via Status through upload without expect.
	raw := rawMsg("Dovecot", "<dovecot@x>", "dovecot body")
	up, err := h.svc.Upload(ctx, h.acc.ID, UploadInput{
		Path: "INBOX", Raw: base64.StdEncoding.EncodeToString(raw),
	})
	if err != nil || up.UID == 0 {
		t.Fatalf("%+v %v", up, err)
	}
	mbxID, msgID := seedIndex(t, h, "INBOX", up.UID, up.UIDValidity, "<dovecot@x>", nil, int64(len(raw)))
	_ = seedMailbox(t, h, "Work", "", up.UIDValidity)

	got, err := h.svc.GetMessage(ctx, h.acc.ID, msgID, GetOptions{Text: "plain", MarkAsSeen: true})
	if err != nil || got.Text == "" {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := h.svc.PatchMessage(ctx, h.acc.ID, msgID, PatchMessageInput{AddFlags: []string{`\Flagged`}}); err != nil {
		t.Fatal(err)
	}
	work, err := h.svc.GetMailbox(ctx, h.acc.ID, ensureMailbox(t, h, "Work", "", up.UIDValidity))
	if err != nil {
		t.Fatal(err)
	}
	if err := h.svc.MoveMessage(ctx, h.acc.ID, msgID, MoveInput{Path: work.Path}); err != nil {
		t.Fatal(err)
	}

	up2, err := h.svc.Upload(ctx, h.acc.ID, UploadInput{
		MailboxID: mbxID, Raw: base64.StdEncoding.EncodeToString(rawMsg("U2", "<u2@x>", "u2")),
		UIDValidity: &up.UIDValidity,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, msg2 := seedIndex(t, h, "INBOX", up2.UID, up2.UIDValidity, "<u2@x>", nil, 10)
	br, err := h.svc.Batch(ctx, h.acc.ID, BatchInput{
		IDs: []string{msg2}, Action: "flags", SetFlags: []string{`\Seen`},
	})
	if err != nil || br.Processed != 1 {
		t.Fatalf("%+v %v", br, err)
	}

	d, err := h.svc.CreateDraft(ctx, h.acc.ID, DraftInput{Subject: "D", Text: "draft on dovecot"})
	if err != nil || d.UID == 0 {
		t.Fatalf("%+v %v", d, err)
	}
	if err := h.svc.DeleteDraft(ctx, h.acc.ID, d.ID); err != nil {
		t.Fatal(err)
	}

	wrong := up.UIDValidity + 999
	_, err = h.svc.Upload(ctx, h.acc.ID, UploadInput{
		Path: "INBOX", Raw: base64.StdEncoding.EncodeToString(raw), UIDValidity: &wrong,
	})
	if errs.CodeOf(err) != errs.CodeUidValidityMismatch {
		t.Fatalf("want mismatch, got %v", err)
	}
}

func TestIntegrationDovecotPoolReuse(t *testing.T) {
	h := dovecotHarness(t)
	ctx := context.Background()
	l1, err := h.pool.Acquire(ctx, h.acc)
	if err != nil {
		t.Fatal(err)
	}
	l1.Release()
	l2, err := h.pool.Acquire(ctx, h.acc)
	if err != nil {
		t.Fatal(err)
	}
	l2.Discard()
}
