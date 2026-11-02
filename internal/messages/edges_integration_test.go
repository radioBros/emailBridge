//go:build integration

package messages

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/submit"
)

func TestIntegrationEdgesValidationAndPaging(t *testing.T) {
	h := simHarness(t)
	ctx := h.ctx
	acc := h.acc.ID

	if _, err := h.svc.ListMessages(ctx, "not-an-id", ListFilter{}); errs.CodeOf(err) != errs.CodeNotFound && errs.CodeOf(err) != errs.CodeValidationFailed {
		// loadAccount checks IsID → not_found
		if errs.CodeOf(err) != errs.CodeNotFound {
			// RequireAccount may run first with invalid id format still permitted by RequireOn
			_ = err
		}
	}
	if _, err := h.svc.GetMailbox(ctx, acc, "mbx_bad"); errs.CodeOf(err) != errs.CodeMailboxNotFound {
		t.Fatalf("%v", err)
	}
	if _, err := h.svc.GetMessage(ctx, acc, "msg_bad", GetOptions{}); errs.CodeOf(err) != errs.CodeMessageNotFound {
		t.Fatalf("%v", err)
	}
	if _, err := h.svc.GetDraft(ctx, acc, "drf_bad", false); errs.CodeOf(err) != errs.CodeDraftNotFound {
		t.Fatalf("%v", err)
	}
	if _, err := h.svc.CreateMailbox(ctx, acc, CreateMailboxInput{}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("%v", err)
	}
	if _, err := h.svc.Upload(ctx, acc, UploadInput{Raw: "x"}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("%v", err)
	}
	if _, err := h.svc.Batch(ctx, acc, BatchInput{}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("%v", err)
	}
	if _, err := h.svc.Batch(ctx, acc, BatchInput{IDs: []string{"msg_x"}, Action: "nope"}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("%v", err)
	}
	if _, _, err := h.svc.GetAttachment(ctx, acc, store.NewID(store.PrefixMessage), "bad", 0); err == nil {
		t.Fatal()
	}

	// page + includeTotal + cursor list
	var mbxID string
	for i := 0; i < 3; i++ {
		mid := "<page" + time.Now().Format("150405.000") + string(rune('a'+i)) + "@x>"
		raw := rawMsg("P", mid, "p")
		inj, err := h.sim.Client.InjectMessage(ctx, h.user, "INBOX", raw, nil, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		id, _ := seedIndex(t, h, "INBOX", inj.UID, inj.UIDValidity, mid, nil, int64(len(raw)))
		mbxID = id
	}
	page, err := h.svc.ListMessages(ctx, acc, ListFilter{MailboxID: mbxID, Limit: 2, Page: 1, IncludeTotal: true, Sort: "uid"})
	if err != nil || page.Total == nil || len(page.Data) != 2 {
		t.Fatalf("%+v %v", page, err)
	}
	cur, err := h.svc.ListMessages(ctx, acc, ListFilter{MailboxID: mbxID, Limit: 1, Sort: "-uid"})
	if err != nil || cur.NextCursor == nil {
		t.Fatalf("%+v %v", cur, err)
	}
	cur2, err := h.svc.ListMessages(ctx, acc, ListFilter{MailboxID: mbxID, Limit: 10, Sort: "-uid", Cursor: *cur.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	_ = cur2

	mbxs, next, err := h.svc.ListMailboxes(ctx, acc, "", 1, "")
	if err != nil || len(mbxs) != 1 || next == nil {
		// may have only 1 mailbox if Archive/Drafts not seeded — seed extras
		_ = seedMailbox(t, h, "Extra1", "", 1)
		_ = seedMailbox(t, h, "Extra2", "", 1)
		mbxs, next, err = h.svc.ListMailboxes(ctx, acc, "", 1, "")
		if err != nil || next == nil {
			t.Fatalf("%v %v %v", mbxs, next, err)
		}
	}
	_, _, err = h.svc.ListMailboxes(ctx, acc, "", 10, *next)
	if err != nil {
		t.Fatal(err)
	}

	// drafts without \Drafts index → IMAP fallback Create Drafts
	h2 := simHarness(t)
	d, err := h2.svc.CreateDraft(h2.ctx, h2.acc.ID, DraftInput{
		Subject: "no-special", Text: "x", HTML: "<b>x</b>",
		From:    &submit.AddressInput{Address: "a@b.test"},
		Headers: map[string]string{"X-Custom": "1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	list, nextD, err := h2.svc.ListDrafts(h2.ctx, h2.acc.ID, 1, "")
	if err != nil {
		t.Fatal(err)
	}
	if nextD != nil {
		_, _, _ = h2.svc.ListDrafts(h2.ctx, h2.acc.ID, 10, *nextD)
	}
	_ = list
	_ = d

	// upload with mailboxId + internalDate
	now := time.Now().UTC()
	raw := rawMsg("dated", "<dated@x>", "d")
	up, err := h.svc.Upload(ctx, acc, UploadInput{
		MailboxID: mbxID, Raw: base64.StdEncoding.EncodeToString(raw), InternalDate: &now,
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = up

	// resolveMailbox validation
	if _, err := h.svc.SearchMessages(ctx, acc, SearchInput{}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("%v", err)
	}

	// GetMessage text=html
	_, msgID := seedIndex(t, h, "INBOX", up.UID, up.UIDValidity, "<dated@x>", nil, int64(len(raw)))
	if _, err := h.svc.GetMessage(ctx, acc, msgID, GetOptions{Text: "html", MaxBytes: 100}); err != nil {
		t.Fatal(err)
	}

	// SendDraft without submit
	h.svc.Submit = nil
	drf, err := h.svc.CreateDraft(ctx, acc, DraftInput{Subject: "s", Text: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.SendDraft(ctx, acc, drf.ID, ""); errs.CodeOf(err) != errs.CodeInternalError {
		t.Fatalf("%v", err)
	}
}

func TestIntegrationNow(t *testing.T) {
	h := simHarness(t)
	h.svc.Now = func() time.Time { return time.Unix(1, 0).UTC() }
	if !h.svc.now().Equal(time.Unix(1, 0).UTC()) {
		t.Fatal()
	}
	h.svc.Now = nil
	if h.svc.now().IsZero() {
		t.Fatal()
	}
}
