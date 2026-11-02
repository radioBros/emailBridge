//go:build integration

package messages

import (
	"context"
	"encoding/base64"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/submit"
)

func TestIntegrationModuleStartStop(t *testing.T) {
	db := testDB(t)
	h := simHarness(t)
	quiet := slog.New(slog.DiscardHandler)
	a := app.New(app.Options{Roles: app.RoleSet{app.RoleAPI: true}, NodeID: "n", Log: quiet})
	app.Provide(a, db)
	app.Provide(a, egress.NewDialer(egress.Policy{}))
	srv, err := httpapi.New(httpapi.Options{AccessLog: quiet, SecurityLog: quiet})
	if err != nil {
		t.Fatal(err)
	}
	app.Provide(a, srv)
	mod := &Module{Accounts: &fakeAccounts{acc: h.acc}, Submit: &fakeSubmit{}, Idle: time.Minute, Log: quiet}
	if err := mod.Start(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if mod.Service() == nil {
		t.Fatal("nil service")
	}
	if err := mod.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	mod2 := &Module{}
	if err := mod2.Start(context.Background(), a); err == nil {
		t.Fatal("want Accounts required")
	}
}

func TestIntegrationHandlersDirect(t *testing.T) {
	h := simHarness(t)
	ctx := h.ctx
	quiet := slog.New(slog.DiscardHandler)
	srv, err := httpapi.New(httpapi.Options{AccessLog: quiet, SecurityLog: quiet})
	if err != nil {
		t.Fatal(err)
	}
	h.svc.Register(srv)

	raw := rawMsg("H", "<h@x>", "handler body")
	inj, err := h.sim.Client.InjectMessage(ctx, h.user, "INBOX", raw, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	mbxID, msgID := seedIndex(t, h, "INBOX", inj.UID, inj.UIDValidity, "<h@x>", nil, int64(len(raw)))
	_ = seedMailbox(t, h, "Archive", "", inj.UIDValidity)
	_ = seedMailbox(t, h, "Drafts", `\Drafts`, 1)
	acc := AccountPath{AccountID: h.acc.ID}

	out, err := h.svc.listMailboxes(ctx, &listMbxIn{AccountPath: acc, Limit: 10})
	if err != nil || len(out.Body.Data) == 0 {
		t.Fatalf("%+v %v", out, err)
	}
	mbxOut, err := h.svc.getMailbox(ctx, &MailboxPath{AccountID: h.acc.ID, MailboxID: mbxID})
	if err != nil || mbxOut.Body.Path != "INBOX" {
		t.Fatalf("%+v %v", mbxOut, err)
	}
	created, err := h.svc.createMailbox(ctx, &createMbxIn{AccountPath: acc, Body: CreateMailboxInput{Path: "Handlers"}})
	if err != nil || created.Status != http.StatusCreated {
		t.Fatalf("%+v %v", created, err)
	}
	folderID := seedMailbox(t, h, "Handlers", "", 1)
	sub := true
	if _, err := h.svc.patchMailbox(ctx, &patchMbxIn{
		MailboxPath: MailboxPath{AccountID: h.acc.ID, MailboxID: folderID},
		Body:        PatchMailboxInput{Subscribed: &sub},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.deleteMailbox(ctx, &MailboxPath{AccountID: h.acc.ID, MailboxID: folderID}); err != nil {
		t.Fatal(err)
	}

	list, err := h.svc.listMessages(ctx, &listMsgIn{AccountPath: acc, MailboxID: mbxID, Limit: 10, Sort: "uid"})
	if err != nil || len(list.Body.Data) == 0 {
		t.Fatalf("%+v %v", list, err)
	}
	got, err := h.svc.getMessage(ctx, &getMsgIn{
		MessagePath: MessagePath{AccountID: h.acc.ID, MessageID: msgID},
		Text:        "both", Headers: true, Envelope: true,
	})
	if err != nil || got.Body.Text == "" {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := h.svc.patchMessage(ctx, &patchMsgIn{
		MessagePath: MessagePath{AccountID: h.acc.ID, MessageID: msgID},
		Body:        PatchMessageInput{AddFlags: []string{`\Seen`}},
	}); err != nil {
		t.Fatal(err)
	}

	mp := multipartMsg("HA", "<ha@x>")
	inj2, err := h.sim.Client.InjectMessage(ctx, h.user, "INBOX", mp, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	_, msgAtt := seedIndex(t, h, "INBOX", inj2.UID, inj2.UIDValidity, "<ha@x>", nil, int64(len(mp)))
	att, err := h.svc.getAttachment(ctx, &attachIn{
		MessagePath: MessagePath{AccountID: h.acc.ID, MessageID: msgAtt}, AttachmentID: "2",
	})
	if err != nil || att == nil || att.Body == nil {
		t.Fatal(err)
	}
	_ = att

	if _, err := h.svc.searchMessages(ctx, &searchIn{AccountPath: acc, Body: SearchInput{MailboxID: mbxID, Subject: "H"}}); err != nil {
		t.Fatal(err)
	}

	up, err := h.svc.uploadMessage(ctx, &uploadIn{
		AccountPath: acc,
		Body:        UploadInput{Path: "INBOX", Raw: base64.StdEncoding.EncodeToString(rawMsg("UH", "<uh@x>", "u"))},
	})
	if err != nil || up.Status != http.StatusCreated {
		t.Fatalf("%+v %v", up, err)
	}

	rawB := rawMsg("BH", "<bh2@x>", "b")
	injB, err := h.sim.Client.InjectMessage(ctx, h.user, "INBOX", rawB, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	_, msgB := seedIndex(t, h, "INBOX", injB.UID, injB.UIDValidity, "<bh2@x>", nil, int64(len(rawB)))
	br, err := h.svc.batchMessages(ctx, &batchIn{
		AccountPath: acc, Body: BatchInput{IDs: []string{msgB}, Action: "flags", AddFlags: []string{`\Seen`}},
	})
	if err != nil || br.Body.Processed != 1 {
		t.Fatalf("%+v %v", br, err)
	}

	rawM := rawMsg("MH", "<mh2@x>", "m")
	injM, err := h.sim.Client.InjectMessage(ctx, h.user, "INBOX", rawM, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	_, msgM := seedIndex(t, h, "INBOX", injM.UID, injM.UIDValidity, "<mh2@x>", nil, int64(len(rawM)))
	if _, err := h.svc.moveMessage(ctx, &moveMsgIn{
		MessagePath: MessagePath{AccountID: h.acc.ID, MessageID: msgM},
		Body:        MoveInput{Path: "Archive"},
	}); err != nil {
		t.Fatal(err)
	}

	rawD := rawMsg("DH", "<dh2@x>", "d")
	injD, err := h.sim.Client.InjectMessage(ctx, h.user, "INBOX", rawD, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	_, msgD := seedIndex(t, h, "INBOX", injD.UID, injD.UIDValidity, "<dh2@x>", nil, int64(len(rawD)))
	if _, err := h.svc.deleteMessage(ctx, &MessagePath{AccountID: h.acc.ID, MessageID: msgD}); err != nil {
		t.Fatal(err)
	}

	dr, err := h.svc.createDraft(ctx, &createDraftIn{
		AccountPath: acc,
		Body: DraftInput{
			From:    &submit.AddressInput{Address: "mario@example.test"},
			Subject: "hd", Text: "handler draft",
		},
	})
	if err != nil || dr.Status != http.StatusCreated {
		t.Fatalf("%+v %v", dr, err)
	}
	ld, err := h.svc.listDrafts(ctx, &listDraftsIn{AccountPath: acc, Limit: 10})
	if err != nil || len(ld.Body.Data) == 0 {
		t.Fatalf("%+v %v", ld, err)
	}
	if _, err := h.svc.getDraft(ctx, &getDraftIn{DraftPath: DraftPath{AccountID: h.acc.ID, DraftID: dr.Body.ID}, Text: true}); err != nil {
		t.Fatal(err)
	}
	sent, err := h.svc.sendDraft(ctx, &sendDraftIn{
		DraftPath: DraftPath{AccountID: h.acc.ID, DraftID: dr.Body.ID}, IdempotencyKey: "k1",
	})
	if err != nil || sent.Status != http.StatusAccepted {
		t.Fatalf("%+v %v", sent, err)
	}

	dr2, err := h.svc.createDraft(ctx, &createDraftIn{AccountPath: acc, Body: DraftInput{Subject: "t", Text: "x"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.deleteDraft(ctx, &DraftPath{AccountID: h.acc.ID, DraftID: dr2.Body.ID}); err != nil {
		t.Fatal(err)
	}

	if _, err := h.svc.getMessage(ctx, &getMsgIn{
		MessagePath: MessagePath{AccountID: h.acc.ID, MessageID: store.NewID(store.PrefixMessage)},
	}); err == nil {
		t.Fatal("want not found")
	}
	if _, err := h.svc.batchMessages(ctx, &batchIn{
		AccountPath: acc, Body: BatchInput{IDs: make([]string, maxBatch+1), Action: "delete"},
	}); err == nil {
		t.Fatal("want batch too large")
	}
}
