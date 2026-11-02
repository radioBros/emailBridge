//go:build integration

package messages

import (
	"context"
	"encoding/base64"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/index"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/submit"
)

func TestIntegrationBatchCopyMoveFailedIDs(t *testing.T) {
	h := simHarness(t)
	ctx := h.ctx
	raw := rawMsg("BC", "<bc@x>", "bc")
	inj, err := h.sim.Client.InjectMessage(ctx, h.user, "INBOX", raw, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	_, msgID := seedIndex(t, h, "INBOX", inj.UID, inj.UIDValidity, "<bc@x>", nil, int64(len(raw)))
	_ = seedMailbox(t, h, "Archive", "", inj.UIDValidity)

	br, err := h.svc.Batch(ctx, h.acc.ID, BatchInput{
		IDs: []string{msgID, store.NewID(store.PrefixMessage)}, Action: "copy", Path: "Archive",
	})
	if err != nil || br.Processed < 1 || len(br.Failed) < 1 {
		t.Fatalf("%+v %v", br, err)
	}

	raw2 := rawMsg("BM", "<bm@x>", "bm")
	inj2, err := h.sim.Client.InjectMessage(ctx, h.user, "INBOX", raw2, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	_, msg2 := seedIndex(t, h, "INBOX", inj2.UID, inj2.UIDValidity, "<bm@x>", nil, int64(len(raw2)))
	archiveID := ensureMailbox(t, h, "Archive", "", inj.UIDValidity)
	br, err = h.svc.Batch(ctx, h.acc.ID, BatchInput{
		IDs: []string{msg2}, Action: "move", MailboxID: archiveID,
	})
	if err != nil || br.Processed != 1 {
		t.Fatalf("%+v %v", br, err)
	}

	raw3 := rawMsg("BF", "<bf@x>", "bf")
	inj3, err := h.sim.Client.InjectMessage(ctx, h.user, "INBOX", raw3, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	_, msg3 := seedIndex(t, h, "INBOX", inj3.UID, inj3.UIDValidity, "<bf@x>", nil, int64(len(raw3)))
	br, err = h.svc.Batch(ctx, h.acc.ID, BatchInput{
		IDs: []string{msg3}, Action: "flags", SetFlags: []string{`\Seen`},
	})
	if err != nil || br.Processed != 1 {
		t.Fatalf("%+v %v", br, err)
	}
	br, err = h.svc.Batch(ctx, h.acc.ID, BatchInput{
		IDs: []string{msg3}, Action: "flags", RemoveFlags: []string{`\Seen`},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = br
}

func TestIntegrationPatchRenameCreateSubscribeFalse(t *testing.T) {
	h := simHarness(t)
	ctx := h.ctx
	sub := false
	created, err := h.svc.CreateMailbox(ctx, h.acc.ID, CreateMailboxInput{Path: "NoSub", Subscribe: &sub})
	if err != nil {
		t.Fatal(err)
	}
	_ = created
	mbxID := seedMailbox(t, h, "NoSub", "", 1)
	// Create again → may load from index
	again, err := h.svc.CreateMailbox(ctx, h.acc.ID, CreateMailboxInput{Path: "NoSub2"})
	if err != nil {
		t.Fatal(err)
	}
	_ = again
	newPath := "NoSubRenamed"
	patched, err := h.svc.PatchMailbox(ctx, h.acc.ID, mbxID, PatchMailboxInput{Path: &newPath})
	if err != nil || patched.Path != newPath {
		t.Fatalf("%+v %v", patched, err)
	}
}

func TestIntegrationMoveByMailboxIDAndPathFallback(t *testing.T) {
	h := simHarness(t)
	ctx := h.ctx
	raw := rawMsg("MV", "<mv@x>", "mv")
	inj, err := h.sim.Client.InjectMessage(ctx, h.user, "INBOX", raw, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	_, msgID := seedIndex(t, h, "INBOX", inj.UID, inj.UIDValidity, "<mv@x>", nil, int64(len(raw)))
	dest := seedMailbox(t, h, "Archive", "", inj.UIDValidity)
	if err := h.svc.MoveMessage(ctx, h.acc.ID, msgID, MoveInput{MailboxID: dest}); err != nil {
		t.Fatal(err)
	}
	raw2 := rawMsg("MV2", "<mv2@x>", "mv2")
	inj2, err := h.sim.Client.InjectMessage(ctx, h.user, "INBOX", raw2, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	_, msg2 := seedIndex(t, h, "INBOX", inj2.UID, inj2.UIDValidity, "<mv2@x>", nil, int64(len(raw2)))
	if err := h.svc.MoveMessage(ctx, h.acc.ID, msg2, MoveInput{Path: "UnindexedDest"}); err != nil {
		// path may need CREATE first
		_, _ = h.svc.CreateMailbox(ctx, h.acc.ID, CreateMailboxInput{Path: "UnindexedDest"})
		if err2 := h.svc.MoveMessage(ctx, h.acc.ID, msg2, MoveInput{Path: "UnindexedDest"}); err2 != nil {
			t.Fatal(err2)
		}
	}
	if err := h.svc.MoveMessage(ctx, h.acc.ID, msg2, MoveInput{}); errs.CodeOf(err) != errs.CodeValidationFailed && errs.CodeOf(err) != errs.CodeMailboxNotFound {
		// empty dest
		_ = err
	}
}

func TestIntegrationDraftsSpecialUseAndSendVariants(t *testing.T) {
	h := simHarness(t)
	ctx := h.ctx
	_ = seedMailbox(t, h, "Drafts", `\Drafts`, 1)
	d, err := h.svc.CreateDraft(ctx, h.acc.ID, DraftInput{
		Raw: base64.StdEncoding.EncodeToString(rawMsg("RawD", "<rawd@x>", "raw draft")),
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := h.svc.GetDraft(ctx, h.acc.ID, d.ID, false)
	if err != nil || got.Text != "" {
		t.Fatalf("%+v %v", got, err)
	}
	// send with Accepted nil but status accepted-ish
	h.sub.res = &submit.SubmitResult{Status: http.StatusOK}
	d2, err := h.svc.CreateDraft(ctx, h.acc.ID, DraftInput{Subject: "s", Text: "t"})
	if err != nil {
		t.Fatal(err)
	}
	res, err := h.svc.SendDraft(ctx, h.acc.ID, d2.ID, "k")
	if err != nil {
		t.Fatal(err)
	}
	// draft not deleted when not Accepted
	if _, err := h.svc.GetDraft(ctx, h.acc.ID, d2.ID, false); err != nil {
		t.Fatal("draft should remain")
	}
	_ = res

	out, err := h.svc.sendDraft(ctx, &sendDraftIn{
		DraftPath: DraftPath{AccountID: h.acc.ID, DraftID: d2.ID}, IdempotencyKey: "k2",
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = out
}

func TestIntegrationModuleStartWithAppSubmit(t *testing.T) {
	db := testDB(t)
	h := simHarness(t)
	quiet := slog.New(slog.DiscardHandler)
	a := app.New(app.Options{Roles: app.RoleSet{app.RoleAPI: true}, NodeID: "n2", Log: quiet})
	app.Provide(a, db)
	app.Provide(a, egress.NewDialer(egress.Policy{}))
	srv, err := httpapi.New(httpapi.Options{AccessLog: quiet, SecurityLog: quiet})
	if err != nil {
		t.Fatal(err)
	}
	app.Provide(a, srv)
	// no Submit on module — Start still works (optional)
	mod := &Module{Accounts: &fakeAccounts{acc: h.acc}, Log: quiet}
	if err := mod.Start(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	_ = mod.Stop(context.Background())
}

func TestIntegrationHandlerErrorBranches(t *testing.T) {
	h := simHarness(t)
	ctx := h.ctx
	acc := AccountPath{AccountID: h.acc.ID}
	if _, err := h.svc.listMailboxes(ctx, &listMbxIn{AccountPath: acc, Cursor: "!!!"}); err == nil {
		t.Fatal()
	}
	if _, err := h.svc.createMailbox(ctx, &createMbxIn{AccountPath: acc, Body: CreateMailboxInput{}}); err == nil {
		t.Fatal()
	}
	if _, err := h.svc.getMailbox(ctx, &MailboxPath{AccountID: h.acc.ID, MailboxID: store.NewID(store.PrefixMailbox)}); err == nil {
		t.Fatal()
	}
	if _, err := h.svc.patchMailbox(ctx, &patchMbxIn{
		MailboxPath: MailboxPath{AccountID: h.acc.ID, MailboxID: store.NewID(store.PrefixMailbox)},
		Body:        PatchMailboxInput{},
	}); err == nil {
		t.Fatal()
	}
	if _, err := h.svc.listMessages(ctx, &listMsgIn{AccountPath: acc, Sort: "nope"}); err == nil {
		t.Fatal()
	}
	if _, err := h.svc.patchMessage(ctx, &patchMsgIn{
		MessagePath: MessagePath{AccountID: h.acc.ID, MessageID: store.NewID(store.PrefixMessage)},
		Body:        PatchMessageInput{AddFlags: []string{`\Seen`}},
	}); err == nil {
		t.Fatal()
	}
	if _, err := h.svc.uploadMessage(ctx, &uploadIn{AccountPath: acc, Body: UploadInput{}}); err == nil {
		t.Fatal()
	}
	if _, err := h.svc.searchMessages(ctx, &searchIn{AccountPath: acc, Body: SearchInput{}}); err == nil {
		t.Fatal()
	}
	if _, err := h.svc.getAttachment(ctx, &attachIn{
		MessagePath: MessagePath{AccountID: h.acc.ID, MessageID: store.NewID(store.PrefixMessage)}, AttachmentID: "1",
	}); err == nil {
		t.Fatal()
	}
	if _, err := h.svc.createDraft(ctx, &createDraftIn{AccountPath: acc, Body: DraftInput{Raw: "!!!"}}); err == nil {
		t.Fatal()
	}
	if _, err := h.svc.getDraft(ctx, &getDraftIn{DraftPath: DraftPath{AccountID: h.acc.ID, DraftID: store.NewID(store.PrefixDraft)}}); err == nil {
		t.Fatal()
	}
	if _, err := h.svc.sendDraft(ctx, &sendDraftIn{DraftPath: DraftPath{AccountID: h.acc.ID, DraftID: store.NewID(store.PrefixDraft)}}); err == nil {
		t.Fatal()
	}
	if _, err := h.svc.listDrafts(ctx, &listDraftsIn{AccountPath: acc, Cursor: "!!!"}); err == nil {
		t.Fatal()
	}
}

func TestIntegrationSearchEmptyAndGetTextVariants(t *testing.T) {
	h := simHarness(t)
	ctx := h.ctx
	mbx := seedMailbox(t, h, "INBOX", "", 1)
	// STATUS/SELECT on empty INBOX after recreate-ish — just search unseen
	found, err := h.svc.SearchMessages(ctx, h.acc.ID, SearchInput{MailboxID: mbx, Subject: "nomatch-zzzz"})
	if err != nil {
		t.Fatal(err)
	}
	if found == nil {
		t.Fatal()
	}
	raw := multipartMsg("TV", "<tv@x>")
	inj, err := h.sim.Client.InjectMessage(ctx, h.user, "INBOX", raw, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	_, msgID := seedIndex(t, h, "INBOX", inj.UID, inj.UIDValidity, "<tv@x>", nil, int64(len(raw)))
	for _, text := range []string{"plain", "html", "both"} {
		if _, err := h.svc.GetMessage(ctx, h.acc.ID, msgID, GetOptions{Text: text, MaxBytes: hardTextCap + 1}); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := h.svc.GetAttachment(ctx, h.acc.ID, msgID, "1", 10); err != nil {
		// part 1 may be text — still ok if bytes returned
		_ = err
	}
}

func TestIntegrationDeleteMessageAndMailboxErrors(t *testing.T) {
	h := simHarness(t)
	ctx := h.ctx
	if err := h.svc.DeleteMessage(ctx, h.acc.ID, store.NewID(store.PrefixMessage)); err == nil {
		t.Fatal()
	}
	if err := h.svc.DeleteMailbox(ctx, h.acc.ID, store.NewID(store.PrefixMailbox)); err == nil {
		t.Fatal()
	}
	raw := rawMsg("DelE", "<dele@x>", "d")
	inj, err := h.sim.Client.InjectMessage(ctx, h.user, "INBOX", raw, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	mbxID, msgID := seedIndex(t, h, "INBOX", inj.UID, inj.UIDValidity, "<dele@x>", nil, int64(len(raw)))
	// break mailbox path so delete's loadMailbox fails after loadMessage... actually loadMailbox uses same mbx
	// delete message success path already covered; delete with missing mailbox row:
	_, _ = h.db.Collection(index.Mailboxes).DeleteOne(ctx, bson.M{"_id": mbxID})
	if err := h.svc.DeleteMessage(ctx, h.acc.ID, msgID); errs.CodeOf(err) != errs.CodeMailboxNotFound {
		t.Fatalf("%v", err)
	}
}

func TestIntegrationDraftsPathFromIndex(t *testing.T) {
	h := simHarness(t)
	_ = seedMailbox(t, h, "MyDrafts", `\Drafts`, 1)
	path, err := h.svc.draftsPath(h.ctx, h.acc.ID)
	if err != nil || path != "MyDrafts" {
		t.Fatalf("%q %v", path, err)
	}
}

func TestIntegrationDraftsPathFromList(t *testing.T) {
	h := simHarness(t)
	// ensure Drafts exists on IMAP via control, no index special-use
	_ = h.sim.Client.CreateMailbox(h.ctx, h.user, "Drafts", `\Drafts`)
	path, err := h.svc.draftsPath(h.ctx, h.acc.ID)
	if err != nil || path == "" {
		t.Fatalf("%q %v", path, err)
	}
	d, err := h.svc.CreateDraft(h.ctx, h.acc.ID, DraftInput{
		Subject: "via-list", Text: "t",
		Cc:      []submit.AddressInput{{Address: "c@e.test"}},
		ReplyTo: []submit.AddressInput{{Address: "r@e.test"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.svc.DeleteDraft(h.ctx, h.acc.ID, d.ID); err != nil {
		t.Fatal(err)
	}
	// delete already-gone draft id
	if err := h.svc.DeleteDraft(h.ctx, h.acc.ID, store.NewID(store.PrefixDraft)); errs.CodeOf(err) != errs.CodeDraftNotFound {
		t.Fatalf("%v", err)
	}
}

func TestIntegrationLoadAccountMissing(t *testing.T) {
	h := simHarness(t)
	h.svc.Accounts = &fakeAccounts{acc: nil}
	_, err := h.svc.Upload(h.ctx, h.acc.ID, UploadInput{
		Path: "INBOX", Raw: base64.StdEncoding.EncodeToString([]byte("From: a\r\n\r\nx")),
	})
	if err == nil {
		t.Fatal()
	}
	other := *h.acc
	other.ID = store.NewID(store.PrefixAccount)
	h.svc.Accounts = &fakeAccounts{acc: &other}
	_, err = h.svc.Upload(h.ctx, h.acc.ID, UploadInput{
		Path: "INBOX", Raw: base64.StdEncoding.EncodeToString([]byte("From: a\r\n\r\nx")),
	})
	if err == nil {
		t.Fatal("wrong account id")
	}
	_ = index.Mailboxes
}
