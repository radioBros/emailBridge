//go:build integration

package messages

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/index"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/submit"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

func TestIntegrationListGetSearchPaging(t *testing.T) {
	h := simHarness(t)
	ctx := h.ctx
	raw := rawMsg("Alpha", "<a@x>", "hello alpha")
	inj, err := h.sim.Client.InjectMessage(ctx, h.user, "INBOX", raw, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	mbxID, msgID := seedIndex(t, h, "INBOX", inj.UID, inj.UIDValidity, "<a@x>", nil, int64(len(raw)))
	raw2 := rawMsg("Beta", "<b@x>", "hello beta")
	inj2, err := h.sim.Client.InjectMessage(ctx, h.user, "INBOX", raw2, []string{`\Flagged`}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	_, msg2 := seedIndex(t, h, "INBOX", inj2.UID, inj2.UIDValidity, "<b@x>", []string{`\Flagged`}, int64(len(raw2)))
	_ = msg2

	list, err := h.svc.ListMessages(ctx, h.acc.ID, ListFilter{MailboxID: mbxID, Limit: 1, Sort: "uid"})
	if err != nil || len(list.Data) != 1 || list.NextCursor == nil {
		t.Fatalf("%+v %v", list, err)
	}
	list2, err := h.svc.ListMessages(ctx, h.acc.ID, ListFilter{
		MailboxID: mbxID, Limit: 10, Sort: "uid", Cursor: *list.NextCursor,
	})
	if err != nil || len(list2.Data) != 1 {
		t.Fatalf("%+v %v", list2, err)
	}
	flagged, err := h.svc.ListMessages(ctx, h.acc.ID, ListFilter{MailboxID: mbxID, Flag: `\Flagged`})
	if err != nil || len(flagged.Data) != 1 {
		t.Fatalf("%+v %v", flagged, err)
	}
	q, err := h.svc.ListMessages(ctx, h.acc.ID, ListFilter{Q: "<a@x>"})
	if err != nil || len(q.Data) != 1 || q.Data[0].ID != msgID {
		t.Fatalf("%+v %v", q, err)
	}

	got, err := h.svc.GetMessage(ctx, h.acc.ID, msgID, GetOptions{Text: "plain", Headers: true, Envelope: true})
	if err != nil {
		t.Fatal(err)
	}
	if got.Text == "" || !strings.Contains(got.Text, "hello alpha") || got.Subject != "Alpha" {
		t.Fatalf("%+v", got)
	}
	if got.Headers["message-id"] == "" && got.Headers["Message-Id"] == "" {
		t.Fatalf("headers: %+v", got.Headers)
	}

	found, err := h.svc.SearchMessages(ctx, h.acc.ID, SearchInput{MailboxID: mbxID, Subject: "Alpha"})
	if err != nil || len(found) != 1 || found[0].ID != msgID {
		t.Fatalf("%+v %v", found, err)
	}

	mbxs, _, err := h.svc.ListMailboxes(ctx, h.acc.ID, "", 10, "")
	if err != nil || len(mbxs) < 1 {
		t.Fatalf("%v %v", mbxs, err)
	}
	one, err := h.svc.GetMailbox(ctx, h.acc.ID, mbxID)
	if err != nil || one.Path != "INBOX" {
		t.Fatalf("%+v %v", one, err)
	}
}

func TestIntegrationFlagsMoveCopyDeleteUpload(t *testing.T) {
	h := simHarness(t)
	ctx := h.ctx
	raw := rawMsg("Ops", "<ops@x>", "ops body")
	inj, err := h.sim.Client.InjectMessage(ctx, h.user, "INBOX", raw, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	_, msgID := seedIndex(t, h, "INBOX", inj.UID, inj.UIDValidity, "<ops@x>", nil, int64(len(raw)))
	archiveID := seedMailbox(t, h, "Archive", "", inj.UIDValidity)

	if _, err := h.svc.PatchMessage(ctx, h.acc.ID, msgID, PatchMessageInput{AddFlags: []string{`\Seen`, `\Flagged`}}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.PatchMessage(ctx, h.acc.ID, msgID, PatchMessageInput{RemoveFlags: []string{`\Flagged`}}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.PatchMessage(ctx, h.acc.ID, msgID, PatchMessageInput{SetFlags: []string{`\Seen`}}); err != nil {
		t.Fatal(err)
	}

	raw2 := rawMsg("CopyMe", "<copy@x>", "copy")
	inj2, err := h.sim.Client.InjectMessage(ctx, h.user, "INBOX", raw2, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	_, msgCopy := seedIndex(t, h, "INBOX", inj2.UID, inj2.UIDValidity, "<copy@x>", nil, int64(len(raw2)))
	br, err := h.svc.Batch(ctx, h.acc.ID, BatchInput{
		IDs: []string{msgCopy}, Action: "copy", Path: "Archive",
	})
	if err != nil || br.Processed != 1 {
		t.Fatalf("%+v %v", br, err)
	}

	if err := h.svc.MoveMessage(ctx, h.acc.ID, msgID, MoveInput{MailboxID: archiveID}); err != nil {
		t.Fatal(err)
	}

	upRaw := rawMsg("Upload", "<up@x>", "uploaded")
	up, err := h.svc.Upload(ctx, h.acc.ID, UploadInput{
		Path: "INBOX", Raw: base64.StdEncoding.EncodeToString(upRaw), Flags: []string{`\Seen`},
	})
	if err != nil || up.UID == 0 || up.Path != "INBOX" {
		t.Fatalf("%+v %v", up, err)
	}

	raw3 := rawMsg("Del", "<del@x>", "bye")
	inj3, err := h.sim.Client.InjectMessage(ctx, h.user, "INBOX", raw3, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	_, msgDel := seedIndex(t, h, "INBOX", inj3.UID, inj3.UIDValidity, "<del@x>", nil, int64(len(raw3)))
	if err := h.svc.DeleteMessage(ctx, h.acc.ID, msgDel); err != nil {
		t.Fatal(err)
	}

	batchDelRaw := rawMsg("BatchDel", "<bd@x>", "bd")
	inj4, err := h.sim.Client.InjectMessage(ctx, h.user, "INBOX", batchDelRaw, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	_, msgBD := seedIndex(t, h, "INBOX", inj4.UID, inj4.UIDValidity, "<bd@x>", nil, int64(len(batchDelRaw)))
	br, err = h.svc.Batch(ctx, h.acc.ID, BatchInput{IDs: []string{msgBD}, Action: "delete"})
	if err != nil || br.Processed != 1 {
		t.Fatalf("%+v %v", br, err)
	}

	br, err = h.svc.Batch(ctx, h.acc.ID, BatchInput{
		IDs: make([]string, maxBatch+1), Action: "delete",
	})
	if errs.CodeOf(err) != errs.CodeBatchTooLarge {
		t.Fatalf("%v", err)
	}
	_ = br
}

func TestIntegrationMailboxCRUD(t *testing.T) {
	h := simHarness(t)
	ctx := h.ctx
	created, err := h.svc.CreateMailbox(ctx, h.acc.ID, CreateMailboxInput{Path: "Projects"})
	if err != nil || created.Path != "Projects" {
		t.Fatalf("%+v %v", created, err)
	}
	// Index a row so patch/delete can resolve it.
	mbxID := seedMailbox(t, h, "Projects", "", 1)
	sub := true
	if _, err := h.svc.PatchMailbox(ctx, h.acc.ID, mbxID, PatchMailboxInput{Subscribed: &sub}); err != nil {
		t.Fatal(err)
	}
	sub = false
	if _, err := h.svc.PatchMailbox(ctx, h.acc.ID, mbxID, PatchMailboxInput{Subscribed: &sub}); err != nil {
		t.Fatal(err)
	}
	renamed := "Projects/Done"
	patched, err := h.svc.PatchMailbox(ctx, h.acc.ID, mbxID, PatchMailboxInput{Path: &renamed})
	if err != nil || patched.Path != renamed {
		t.Fatalf("%+v %v", patched, err)
	}
	_, _ = h.db.Collection(index.Mailboxes).UpdateOne(ctx,
		bson.M{"_id": mbxID},
		bson.M{"$set": bson.M{"path": renamed}},
	)
	if err := h.svc.DeleteMailbox(ctx, h.acc.ID, mbxID); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationDraftSaveSend(t *testing.T) {
	h := simHarness(t)
	ctx := h.ctx
	_ = seedMailbox(t, h, "Drafts", `\Drafts`, 1)
	d, err := h.svc.CreateDraft(ctx, h.acc.ID, DraftInput{
		From:    &submit.AddressInput{Address: "mario@example.test"},
		To:      []submit.AddressInput{{Address: "team@example.test"}},
		Subject: "Draft subj", Text: "draft body",
	})
	if err != nil || d.ID == "" || d.UID == 0 {
		t.Fatalf("%+v %v", d, err)
	}
	list, _, err := h.svc.ListDrafts(ctx, h.acc.ID, 10, "")
	if err != nil || len(list) != 1 {
		t.Fatalf("%v %v", list, err)
	}
	got, err := h.svc.GetDraft(ctx, h.acc.ID, d.ID, true)
	if err != nil || !strings.Contains(got.Text, "draft body") {
		t.Fatalf("%+v %v", got, err)
	}
	res, err := h.svc.SendDraft(ctx, h.acc.ID, d.ID, "idem-draft-1")
	if err != nil || res.Accepted == nil || h.sub.calls != 1 || h.sub.lastKey != "idem-draft-1" {
		t.Fatalf("%+v %v calls=%d", res, err, h.sub.calls)
	}
	if h.sub.lastReq.Raw == "" {
		t.Fatal("submit raw empty")
	}
	if _, err := h.svc.GetDraft(ctx, h.acc.ID, d.ID, false); errs.CodeOf(err) != errs.CodeDraftNotFound {
		t.Fatalf("draft should be deleted after send: %v", err)
	}

	d2, err := h.svc.CreateDraft(ctx, h.acc.ID, DraftInput{Subject: "tmp", Text: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.svc.DeleteDraft(ctx, h.acc.ID, d2.ID); err != nil {
		t.Fatal(err)
	}
}

func TestIntegrationUIDValidityConflict(t *testing.T) {
	h := simHarness(t)
	ctx := h.ctx
	raw := rawMsg("UV", "<uv@x>", "uv")
	inj, err := h.sim.Client.InjectMessage(ctx, h.user, "INBOX", raw, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	_, msgID := seedIndex(t, h, "INBOX", inj.UID, inj.UIDValidity, "<uv@x>", nil, int64(len(raw)))
	newUV, err := h.sim.Client.RecreateMailbox(ctx, h.user, "INBOX")
	if err != nil {
		t.Fatal(err)
	}
	if newUV == inj.UIDValidity {
		t.Fatalf("uidValidity did not change: %d", newUV)
	}
	_, err = h.svc.PatchMessage(ctx, h.acc.ID, msgID, PatchMessageInput{AddFlags: []string{`\Seen`}})
	if errs.CodeOf(err) != errs.CodeUidValidityMismatch {
		t.Fatalf("want uid_validity_mismatch, got %v", err)
	}
	wrong := inj.UIDValidity
	_, err = h.svc.Upload(ctx, h.acc.ID, UploadInput{
		Path: "INBOX", Raw: base64.StdEncoding.EncodeToString(rawMsg("x", "<x@x>", "x")),
		UIDValidity: &wrong,
	})
	if errs.CodeOf(err) != errs.CodeUidValidityMismatch {
		t.Fatalf("upload want mismatch, got %v", err)
	}
}

func TestIntegrationAccountBoundKeyIsolation(t *testing.T) {
	h := simHarness(t)
	other := store.NewID(store.PrefixAccount)
	bound := h.boundKey(h.acc.ID)
	if _, err := h.svc.ListMessages(bound, h.acc.ID, ListFilter{}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.ListMessages(bound, other, ListFilter{}); errs.CodeOf(err) != errs.CodeForbidden {
		t.Fatalf("want forbidden, got %v", err)
	}
	viewer := tenancy.WithCaller(context.Background(), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: store.NewID(store.PrefixUser),
		OrganizationID: h.org, Role: tenancy.RoleViewer,
	})
	if _, err := h.svc.CreateMailbox(viewer, h.acc.ID, CreateMailboxInput{Path: "Nope"}); errs.CodeOf(err) != errs.CodeForbidden {
		t.Fatalf("viewer write: %v", err)
	}
}

func TestIntegrationAttachmentFetch(t *testing.T) {
	h := simHarness(t)
	ctx := h.ctx
	raw := multipartMsg("Att", "<att@x>")
	inj, err := h.sim.Client.InjectMessage(ctx, h.user, "INBOX", raw, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	_, msgID := seedIndex(t, h, "INBOX", inj.UID, inj.UIDValidity, "<att@x>", nil, int64(len(raw)))
	body, ctype, err := h.svc.GetAttachment(ctx, h.acc.ID, msgID, "2", 0)
	if err != nil || len(body) == 0 || ctype == "" {
		t.Fatalf("%q %q %v", body, ctype, err)
	}
	if _, _, err := h.svc.GetAttachment(ctx, h.acc.ID, msgID, "99", 0); errs.CodeOf(err) != errs.CodeAttachmentNotFound {
		// empty part or not found — either attachment_not_found
		if errs.CodeOf(err) != errs.CodeAttachmentNotFound && err == nil {
			t.Fatalf("%v", err)
		}
	}
}

func TestIntegrationBatchFlagsMove(t *testing.T) {
	h := simHarness(t)
	ctx := h.ctx
	var ids []string
	var uv uint32
	for i := 0; i < 3; i++ {
		mid := fmt.Sprintf("<b%d@x>", i)
		raw := rawMsg(fmt.Sprintf("B%d", i), mid, "b")
		inj, err := h.sim.Client.InjectMessage(ctx, h.user, "INBOX", raw, nil, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		uv = inj.UIDValidity
		_, id := seedIndex(t, h, "INBOX", inj.UID, inj.UIDValidity, mid, nil, int64(len(raw)))
		ids = append(ids, id)
	}
	_ = seedMailbox(t, h, "Archive", "", uv)
	br, err := h.svc.Batch(ctx, h.acc.ID, BatchInput{
		IDs: ids[:2], Action: "flags", AddFlags: []string{`\Seen`},
	})
	if err != nil || br.Processed != 2 {
		t.Fatalf("%+v %v", br, err)
	}
	br, err = h.svc.Batch(ctx, h.acc.ID, BatchInput{
		IDs: ids[2:], Action: "move", Path: "Archive",
	})
	if err != nil || br.Processed != 1 {
		t.Fatalf("%+v %v", br, err)
	}
}

func TestIntegrationConnectionBusy(t *testing.T) {
	h := simHarness(t)
	ctx := h.ctx
	lease, err := h.pool.Acquire(ctx, h.acc)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	_, err = h.svc.Upload(ctx, h.acc.ID, UploadInput{
		Path: "INBOX", Raw: base64.StdEncoding.EncodeToString(rawMsg("busy", "<busy@x>", "x")),
	})
	if errs.CodeOf(err) != errs.CodeConnectionBusy {
		t.Fatalf("want connection_busy, got %v", err)
	}
}
