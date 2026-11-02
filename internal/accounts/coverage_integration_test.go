//go:build integration

package accounts

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/objstore"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

func TestIntegrationAPIHandlersAndPatchPaths(t *testing.T) {
	svc, ctx, org := testSvc(t)
	mod := &Module{svc: svc, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}

	// create via handler (error + success)
	if _, err := mod.createAccount(ctx, &createIn{Body: CreateInput{Name: ""}}); err == nil {
		t.Fatal("create validation")
	}
	if _, err := mod.getAccount(ctx, &accountIDIn{AccountID: "acc_notavalidulidxxxxxxxxxxxx0"}); err == nil {
		t.Fatal("get missing")
	}
	if _, err := mod.verifyAccount(ctx, &verifyIn{Body: VerifyRequest{}}); err == nil {
		t.Fatal("verify empty")
	}
	if _, err := mod.deleteAccount(ctx, &deleteIn{AccountID: "acc_notavalidulidxxxxxxxxxxxx0"}); err == nil {
		t.Fatal("delete missing")
	}
	if _, err := mod.syncAccount(ctx, &syncIn{AccountID: "acc_notavalidulidxxxxxxxxxxxx0", Body: SyncInput{Mode: "fast"}}); err == nil {
		t.Fatal("sync missing")
	}
	if _, err := mod.reconnectAccount(ctx, &accountIDOnly{AccountID: "acc_notavalidulidxxxxxxxxxxxx0"}); err == nil {
		t.Fatal("reconnect missing")
	}
	if _, err := mod.resumeAccount(ctx, &accountIDOnly{AccountID: "acc_notavalidulidxxxxxxxxxxxx0"}); err == nil {
		t.Fatal("resume missing")
	}
	if _, err := mod.patchAccount(ctx, &patchIn{AccountID: "acc_notavalidulidxxxxxxxxxxxx0", Body: patchBody{ExternalID: json.RawMessage(`{`)}}); err == nil {
		t.Fatal("patch bad json")
	}

	in := sampleCreate("api-1")
	in.Sync = &SyncConfig{Paths: []string{"INBOX"}, IdlePaths: []string{"INBOX"}}
	out, err := mod.createAccount(ctx, &createIn{Body: in})
	if err != nil || out.Status != 201 {
		t.Fatalf("%+v %v", out, err)
	}
	id := out.Body.ID

	if _, err := mod.getAccount(ctx, &accountIDIn{AccountID: id}); err != nil {
		t.Fatal(err)
	}
	list, err := mod.listAccounts(ctx, &listIn{Sort: "-createdAt", Q: "Test", State: StateNew})
	if err != nil || len(list.Body.Data) < 1 {
		t.Fatalf("%+v %v", list, err)
	}

	// page + total
	list2, err := mod.listAccounts(ctx, &listIn{Page: 1, Limit: 1, IncludeTotal: true, Sort: "email"})
	if err != nil || list2.Body.Total == nil {
		t.Fatalf("%+v %v", list2, err)
	}

	// cursor pagination: create more, list limit 1
	_, _ = svc.Create(ctx, sampleCreate("api-2"))
	_, _ = svc.Create(ctx, sampleCreate("api-3"))
	page1, err := svc.List(ctx, ListFilter{Limit: 1, Sort: "id"})
	if err != nil || page1.NextCursor == nil {
		t.Fatalf("%+v %v", page1, err)
	}
	page2, err := svc.List(ctx, ListFilter{Limit: 1, Sort: "id", Cursor: *page1.NextCursor})
	if err != nil || len(page2.Data) < 1 {
		t.Fatalf("%+v %v", page2, err)
	}

	// sortKey branches
	_ = sortKey(Account{Name: "n", Email: "e", State: StateNew, CreatedAt: time.Now(), LastSyncAt: ptrTime(time.Now())}, "name")
	_ = sortKey(Account{Email: "e"}, "email")
	_ = sortKey(Account{State: StateNew}, "state")
	_ = sortKey(Account{CreatedAt: time.Now()}, "createdAt")
	_ = sortKey(Account{}, "lastSyncAt")
	_ = sortKey(Account{ID: "x"}, "id")

	// patch via handler: name, email, sync, tz, locale, switches, imap/smtp pass, externalId null
	email := "patched@example.com"
	tz, loc := "Europe/Rome", "it-IT"
	dis := false
	_ = dis
	pout, err := mod.patchAccount(ctx, &patchIn{
		AccountID: id, IfMatch: formatETag(out.Body.Version),
		Body: patchBody{
			Email: &email, TZ: &tz, Locale: &loc,
			Sync:       &SyncConfig{Paths: []string{"INBOX"}, IdlePaths: []string{"INBOX"}, ResyncDelay: "60s", MaxIdle: "5m"},
			Switches:   map[string]any{"inbound.archiveRaw": true},
			IMAP:       &IMAPConfigInput{Host: "127.0.0.1", Port: 993, TLS: TLSImplicit, Auth: MailAuthInput{User: "u2", Pass: "newpass"}},
			SMTP:       &SMTPConfigInput{Host: "127.0.0.1", Port: 465, TLS: TLSImplicit, Auth: MailAuthInput{User: "u2", Pass: "newpass"}, Policy: PolicyAtMostOnce, MaxAttempts: intPtr(3), BackoffBase: "1s"},
			ExternalID: json.RawMessage(`null`),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if pout.Body.ExternalID != nil {
		t.Fatal("externalId should be cleared")
	}

	// set externalId again
	ext := "reext"
	_, err = mod.patchAccount(ctx, &patchIn{
		AccountID: id, Body: patchBody{ExternalID: json.RawMessage(`"reext"`), Name: strPtr("N2")},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = ext

	// disable
	trueV := true
	_, err = mod.patchAccount(ctx, &patchIn{AccountID: id, Body: patchBody{Disabled: &trueV}})
	if err != nil {
		t.Fatal(err)
	}
	rout, err := mod.resumeAccount(ctx, &accountIDOnly{AccountID: id})
	if err != nil || rout.Body.State != StateConnecting {
		t.Fatalf("%+v %v", rout, err)
	}

	sout, err := mod.syncAccount(ctx, &syncIn{AccountID: id, Body: SyncInput{Mode: "fast", Paths: []string{"INBOX"}}})
	if err != nil || sout.Status != 202 {
		t.Fatalf("%+v %v", sout, err)
	}
	rcout, err := mod.reconnectAccount(ctx, &accountIDOnly{AccountID: id})
	if err != nil || rcout.Status != 202 {
		t.Fatalf("%+v %v", rcout, err)
	}

	vout, err := mod.verifyAccount(ctx, &verifyIn{Body: VerifyRequest{
		SMTP: &SMTPConfigInput{Host: "127.0.0.1", Port: 1, TLS: TLSNone, Auth: MailAuthInput{User: "u", Pass: "p"}},
	}})
	if err != nil || vout.Body.SMTP == nil || vout.Body.SMTP.OK {
		t.Fatalf("expected smtp fail: %+v %v", vout, err)
	}

	// delete via handler
	acc, _ := svc.Get(ctx, id)
	dout, err := mod.deleteAccount(ctx, &deleteIn{AccountID: id, IfMatch: formatETag(acc.Version)})
	if err != nil || dout.Status != 202 {
		t.Fatalf("%+v %v", dout, err)
	}

	// quota
	svc.QuotaAccounts = 1
	if _, err := svc.Create(ctx, sampleCreate("quota")); errs.CodeOf(err) != errs.CodeQuotaAccountsExceeded {
		// may succeed if deleted account still counted — create another org account first
		a2, err2 := svc.Create(ctx, sampleCreate("keep"))
		if err2 != nil {
			t.Fatal(err2)
		}
		if _, err := svc.Create(ctx, sampleCreate("over")); errs.CodeOf(err) != errs.CodeQuotaAccountsExceeded {
			t.Fatalf("quota: %v (a2=%s)", err, a2.ID)
		}
	}
	svc.QuotaAccounts = 0

	// Roles()
	if len(mod.Roles()) == 0 {
		t.Fatal("roles")
	}

	// decryptPass
	acc2, _ := svc.Create(ctx, sampleCreate("dec"))
	pass, err := svc.decryptPass(ctx, acc2.ID, "imap.auth.pass", acc2.IMAP.Auth.Pass)
	if err != nil || pass != "secret-pass" {
		t.Fatalf("%q %v", pass, err)
	}

	if st, err := objstore.Open(objstore.Options{
		Endpoint: "http://127.0.0.1:1", Bucket: "b", AccessKey: "a", SecretKey: "s", PathStyle: true,
	}); err == nil {
		svc.Objects = st
		_, _ = svc.eraseObjects(ctx, org, acc2.ID)
	}

	// list bad state / sort
	if _, err := svc.List(ctx, ListFilter{State: "nope"}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	if _, err := mod.listAccounts(ctx, &listIn{Sort: "nope"}); errs.CodeOf(err) != errs.CodeSortNotAllowed {
		t.Fatal(err)
	}

	// version mismatch
	if _, err := svc.Patch(ctx, acc2.ID, PatchInput{Name: strPtr("x")}, `"999"`); errs.CodeOf(err) != errs.CodeVersionMismatch {
		t.Fatal(err)
	}

	// Transition illegal
	acc2.Owner = &Owner{NodeID: "n", Epoch: 1, Since: time.Now()}
	acc2.State = StateNew
	_, _ = svc.coll().ReplaceOne(ctx, bson.M{"_id": acc2.ID}, acc2)
	if _, err := svc.Transition(ctx, acc2.ID, 1, StateSyncing, nil); errs.CodeOf(err) != errs.CodeConflict {
		t.Fatal(err)
	}

	// publishSyncCmd with owner
	acc2.Owner = &Owner{NodeID: "n1", Epoch: 1, Since: time.Now()}
	svc.publishSyncCmd(acc2, CmdResync, nil)

	// redactServerText + takeVerifyToken rate limit
	_ = redactServerText(strings.Repeat("x", 600))
	_ = redactServerText("user password=foo")
	for i := 0; i < 6; i++ {
		_ = svc.takeVerifyToken(context.Background(), org)
	}
	if err := svc.takeVerifyToken(context.Background(), org); errs.CodeOf(err) != errs.CodeRateLimited {
		t.Fatalf("rate limit: %v", err)
	}

	// validate more
	_ = validateSMTPInput(SMTPConfigInput{Host: "h", Port: 25, TLS: TLSNone, Auth: MailAuthInput{User: "u"}, Policy: PolicyStandard, MaxAttempts: intPtr(0)}, false)
	_ = validateSMTPInput(SMTPConfigInput{Host: "h", Port: 25, TLS: TLSNone, Auth: MailAuthInput{User: "u"}, BackoffBase: "bad"}, false)
	_ = validateIMAPInput(IMAPConfigInput{Host: "h", Port: 1, TLS: TLSNone, Auth: MailAuthInput{}}, true)
	_ = validateSync(&SyncConfig{MaxIdle: "bad"})
	_ = validatePath("")
	_ = validateSwitches(map[string]any{"auth.password": true}, nil) // not account scope
	_ = validateEmail("")
	_ = itoa(-3)
	_ = parseETag("12")
	_ = trimETag("W/\"1\"")
	_ = svc.log()
	_ = svc.registry()
	svc.Log = nil
	_ = svc.log()
	svc.Registry = nil
	_ = svc.registry()
	svc.Egress = nil
	_, _ = svc.dialMail(ctx, "tcp", "127.0.0.1:1")
	js := svc.Jobs
	svc.Jobs = nil
	svc.RegisterHandlers()
	svc.Jobs = js
	svc.audit(ctx, "x", "y", nil)
	svc.Audit = nil
	svc.audit(ctx, "x", "y", nil)
	_ = extPtr("")
	_, _ = decodeCursor("%%%")
	_, _ = cursorFilter(cursorPayload{ID: "a", Key: "2020-01-01T00:00:00Z", Field: "createdAt"}, "createdAt", 1)
	_, _ = cursorFilter(cursorPayload{ID: "a", Key: "bad"}, "createdAt", -1)

	// Sync/Reconnect deleting
	del := sampleCreate("delstate")
	ad, _ := svc.Create(ctx, del)
	ad.State = StateDeleting
	_, _ = svc.coll().ReplaceOne(ctx, bson.M{"_id": ad.ID}, ad)
	if _, err := svc.Sync(ctx, ad.ID, SyncInput{Mode: "fast"}); errs.CodeOf(err) != errs.CodeAccountDeleting {
		t.Fatal(err)
	}
	if _, err := svc.Reconnect(ctx, ad.ID); errs.CodeOf(err) != errs.CodeAccountDeleting {
		t.Fatal(err)
	}
	if _, err := svc.Sync(ctx, ad.ID, SyncInput{Mode: "nope"}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}

	// Create with verify failure (skip false) against closed port — may be slow
	c := sampleCreate("verfail")
	c.SkipVerify = false
	c.ExternalID = "verfail"
	c.SMTP.Port = 1
	c.IMAP.Port = 1
	c.IMAP.TLS = TLSNone
	c.SMTP.TLS = TLSNone
	if _, err := svc.Create(ctx, c); err == nil {
		t.Fatal("create should fail verify")
	}

	// account-bound key cannot create
	bound := tenancy.WithCaller(context.Background(), tenancy.Caller{
		Kind: tenancy.CallerAPIKey, APIKeyID: store.NewID(store.PrefixAPIKey),
		OrganizationID: org, Scopes: []string{"accounts:write"}, AccountID: acc2.ID,
	})
	if _, err := svc.Create(bound, sampleCreate("nok")); errs.CodeOf(err) != errs.CodeForbidden {
		t.Fatal(err)
	}

	// load bad id
	if _, err := svc.load(ctx, "acc_notavalidulidxxxxxxxxxxxx0"); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatal(err)
	}
}

func strPtr(s string) *string { return &s }
func intPtr(i int) *int       { return &i }
func ptrTime(t time.Time) *time.Time {
	return &t
}
