//go:build integration

package deliverytest

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/jobs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/submit"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

type failSubmit struct{}

func (failSubmit) Submit(context.Context, string, submit.SubmitRequest, string, bool) (*submit.SubmitResult, error) {
	return nil, errs.New(errs.CodeSMTPPermanent, "refused")
}

func TestIntegrationCoverageExtras(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	_ = EnsureIndexes(ctx, db)
	_ = jobs.Migration(1).Up(ctx, db)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	js := &jobs.Service{DB: db, NodeID: "n1", Log: quiet}
	org := store.NewID(store.PrefixOrganization)
	acc := store.NewID(store.PrefixAccount)
	cctx := tenancy.WithCaller(store.WithOrganization(ctx, org), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: "u", OrganizationID: org, Role: tenancy.RoleOwner,
	})

	// FindByMessageID path
	mbxID := store.NewID(store.PrefixMailbox)
	msgID := store.NewID(store.PrefixMessage)
	_, _ = db.Collection("mailboxes").InsertOne(ctx, bson.M{
		"_id": mbxID, "organizationId": org, "accountId": acc, "path": "Junk", "specialUse": `\Junk`, "status": "active",
	})
	_, _ = db.Collection("messages").InsertOne(ctx, bson.M{
		"_id": msgID, "organizationId": org, "accountId": acc, "mailboxId": mbxID,
		"messageIdHeader": "<hit@test>", "internalDate": time.Now().UTC(),
	})
	hit, err := FindByMessageID(ctx, db, acc, "<hit@test>")
	if err != nil || hit == nil || classifyFolder(hit.MailboxPath, hit.SpecialUse) != FolderSpam {
		t.Fatalf("find: %v %+v", err, hit)
	}
	miss, err := FindByMessageID(ctx, db, acc, "<missing@test>")
	if err != nil || miss != nil {
		t.Fatalf("miss: %v %+v", err, miss)
	}

	svc := &Service{Repo: &Repo{DB: db}, Jobs: js, Submit: &memSubmit{}, Env: "development", PollEvery: 5 * time.Millisecond}
	svc.RegisterHandlers()
	mod := &Module{Env: "development", Log: quiet, svc: svc}
	if mod.Name() == "" || len(mod.Roles()) == 0 || mod.Service() == nil {
		t.Fatal("module")
	}
	_ = mod.Stop(ctx)

	accp, err := svc.Create(cctx, CreateInput{AccountID: acc, To: "a@b.com", Timeout: "1s"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mod.get(cctx, &idIn{TestID: accp.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := mod.create(cctx, &createIn{Body: CreateInput{AccountID: acc, To: "c@d.com"}}); err != nil {
		t.Fatal(err)
	}

	// fail path via submit error
	svcFail := &Service{Repo: &Repo{DB: db}, Jobs: js, Submit: failSubmit{}, Env: "development"}
	svcFail.RegisterHandlers()
	accFail, err := svcFail.Create(cctx, CreateInput{AccountID: acc, To: "e@f.com", Timeout: "1s"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = js.RunNow(cctx, JobKind, org, jobPayload{TestID: accFail.ID}, nil)
	if err == nil {
		t.Fatal("expected fail")
	}
	got, _ := svcFail.Get(cctx, accFail.ID)
	if got.State != StateFailed {
		t.Fatalf("state %s", got.State)
	}

	// find() default + loadRaw nil / authFromRaw edges
	svc2 := &Service{Repo: &Repo{DB: db}, Now: func() time.Time { return time.Now().UTC() }}
	if _, err := svc2.find(ctx, acc, "<hit@test>"); err != nil {
		t.Fatal(err)
	}
	_ = svc2.loadRaw(ctx, acc, msgID, "")
	_ = authFromRaw([]byte("not-a-message"), nil)
	_ = authFromRaw([]byte("From: a@b.com\r\n\r\n"), nil)
	_ = classifyFolder("INBOX.Spam", "")
	_ = classifyFolder("folder/inbox", "")
	_ = encodeProblem(nil)

	a := app.New(app.Options{Roles: app.RoleSet{app.RoleAPI: true}, NodeID: "n", Log: quiet})
	app.Provide(a, db)
	app.Provide(a, js)
	app.Provide(a, &submit.Service{})
	srv, err := httpapi.New(httpapi.Options{AccessLog: quiet, SecurityLog: quiet})
	if err != nil {
		t.Fatal(err)
	}
	app.Provide(a, srv)
	m2 := &Module{Env: "development", Log: quiet}
	if err := m2.Start(ctx, a); err != nil {
		t.Fatal(err)
	}
	_ = m2.Stop(ctx)

	if _, err := svc.Get(cctx, "dtt_bad"); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatal(err)
	}
	if _, err := mod.get(cctx, &idIn{TestID: "dtt_bad"}); err == nil {
		t.Fatal("api get missing")
	}
	_, _ = parseTimeout("bad")
	_, _ = parseTimeout("0s")
	svc.Env = "production"
	svc.TestRecipient = "only@test"
	if err := svc.checkRecipient("other@test"); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	// idempotent succeeded re-run
	svcOK := &Service{
		Repo: &Repo{DB: db}, Jobs: js, Submit: &memSubmit{mid: "<hit@test>"}, Env: "development",
		PollEvery: 5 * time.Millisecond,
		Find: func(context.Context, string, string) (*Hit, error) {
			return &Hit{MessageID: msgID, MailboxPath: "INBOX", SpecialUse: `\Inbox`}, nil
		},
	}
	svcOK.RegisterHandlers()
	okAcc, err := svcOK.Create(cctx, CreateInput{AccountID: acc, To: "z@z.com", Timeout: "2s"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := js.RunNow(cctx, JobKind, org, jobPayload{TestID: okAcc.ID}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := js.RunNow(cctx, JobKind, org, jobPayload{TestID: okAcc.ID}, nil); err != nil {
		t.Fatal(err)
	}
	_ = authFromRaw([]byte("Authentication-Results: bad;;;\r\nFrom: a@b.com\r\n\r\n"), []string{"good"})
	_ = classifyFolder("Spam", "")
	_ = classifyFolder("Bulk", "")
	_ = classifyFolder("INBOX/Spam", "")
	_ = classifyFolder("x.inbox", "")

	// bad job payload + cancelled + GetByID missing
	_, err = js.RunNow(cctx, JobKind, org, map[string]any{}, nil)
	_ = err
	_, err = js.RunNow(cctx, JobKind, org, jobPayload{TestID: store.NewID(store.PrefixDeliveryTest)}, nil)
	if errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatalf("missing test: %v", err)
	}
	svcCancel := &Service{
		Repo: &Repo{DB: db}, Jobs: js, Submit: &memSubmit{}, Env: "development",
		PollEvery: time.Hour,
		Find:      func(context.Context, string, string) (*Hit, error) { return nil, nil },
	}
	svcCancel.RegisterHandlers()
	cAcc, err := svcCancel.Create(cctx, CreateInput{AccountID: acc, To: "cancel@t.com", Timeout: "5s"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = js.RunNow(cctx, JobKind, org, jobPayload{TestID: cAcc.ID}, func() bool { return true })
	if err == nil {
		t.Fatal("cancelled")
	}
	svc.LoadRaw = func(context.Context, string, string, string) ([]byte, error) {
		return nil, errs.New(errs.CodeInternalError, "no raw")
	}
	_ = svc.loadRaw(ctx, acc, msgID, "ref")
	if _, err := svc.Create(cctx, CreateInput{AccountID: store.NewID(store.PrefixAccount), To: ""}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	svcNo := &Service{Repo: &Repo{DB: db}, Env: "development"}
	if _, err := svcNo.Create(cctx, CreateInput{AccountID: acc, To: "a@b.com"}); errs.CodeOf(err) != errs.CodeInternalError {
		t.Fatal(err)
	}
}
