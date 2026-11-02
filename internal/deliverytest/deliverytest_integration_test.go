//go:build integration

package deliverytest

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/jobs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/submit"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

type memSubmit struct {
	mid string
	oid string
}

func (m *memSubmit) Submit(_ context.Context, _ string, _ submit.SubmitRequest, _ string, _ bool) (*submit.SubmitResult, error) {
	if m.mid == "" {
		m.mid = "<probe@delivery.test>"
	}
	if m.oid == "" {
		m.oid = store.NewID(store.PrefixOutbound)
	}
	return &submit.SubmitResult{Accepted: &submit.Accepted{ID: m.oid, MessageID: m.mid, State: "queued"}}, nil
}

func testDB(t *testing.T) *store.DB {
	t.Helper()
	uri := os.Getenv("EB_TEST_MONGO_URI")
	if uri == "" {
		uri = "mongodb://127.0.0.1:27017/?replicaSet=rs0&directConnection=true"
	}
	ctx := context.Background()
	db, err := store.Connect(ctx, store.Options{
		URI: uri, Database: fmt.Sprintf("ebtest_dtt_%d", time.Now().UnixNano()), ConnectTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Skipf("no MongoDB: %v", err)
	}
	t.Cleanup(func() { _ = db.DB.Drop(ctx); _ = db.Close(ctx) })
	return db
}

func TestIntegrationDeliveryTestE2E(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	if err := EnsureIndexes(ctx, db); err != nil {
		t.Fatal(err)
	}
	if err := jobs.Migration(1).Up(ctx, db); err != nil {
		t.Fatal(err)
	}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	js := &jobs.Service{DB: db, NodeID: "n1", Log: quiet}
	org := store.NewID(store.PrefixOrganization)
	acc := store.NewID(store.PrefixAccount)
	cctx := tenancy.WithCaller(store.WithOrganization(ctx, org), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: store.NewID(store.PrefixUser), OrganizationID: org, Role: tenancy.RoleOwner,
	})
	sub := &memSubmit{}
	svc := &Service{
		Repo: &Repo{DB: db}, Jobs: js, Submit: sub, Env: "development",
		PollEvery: 20 * time.Millisecond,
	}
	svc.RegisterHandlers()

	hitAt := time.Now().Add(80 * time.Millisecond)
	svc.Find = func(_ context.Context, _, mid string) (*Hit, error) {
		if time.Now().Before(hitAt) {
			return nil, nil
		}
		if mid != sub.mid {
			return nil, nil
		}
		return &Hit{MessageID: store.NewID(store.PrefixMessage), MailboxPath: "INBOX", SpecialUse: `\Inbox`}, nil
	}
	svc.LoadRaw = func(context.Context, string, string, string) ([]byte, error) {
		return []byte("Authentication-Results: mx.test; spf=pass smtp.mailfrom=a@b.com\r\nFrom: a@b.com\r\n\r\nx\r\n"), nil
	}
	svc.TrustedAR = []string{"mx.test"}

	accp, err := svc.Create(cctx, CreateInput{AccountID: acc, To: "probe@example.com", Timeout: "2s"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := js.RunNow(cctx, JobKind, org, jobPayload{TestID: accp.ID}, nil); err != nil {
		t.Fatal(err)
	}

	got, err := svc.Get(cctx, accp.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateSucceeded || got.Folder != FolderInbox || got.AuthResults == nil {
		t.Fatalf("result: %+v auth=%+v", got, got.AuthResults)
	}
	if got.OutboundID == "" || got.LatencyMs < 0 {
		t.Fatalf("ids/latency: %+v", got)
	}
}

func TestIntegrationDeliveryTestTimeout(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	_ = EnsureIndexes(ctx, db)
	_ = jobs.Migration(1).Up(ctx, db)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	js := &jobs.Service{DB: db, NodeID: "n1", Log: quiet}
	org := store.NewID(store.PrefixOrganization)
	cctx := tenancy.WithCaller(store.WithOrganization(ctx, org), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: "u", OrganizationID: org, Role: tenancy.RoleOwner,
	})
	svc := &Service{
		Repo: &Repo{DB: db}, Jobs: js, Submit: &memSubmit{}, Env: "development",
		PollEvery: 10 * time.Millisecond, Find: func(context.Context, string, string) (*Hit, error) { return nil, nil },
	}
	svc.RegisterHandlers()
	accp, err := svc.Create(cctx, CreateInput{
		AccountID: store.NewID(store.PrefixAccount), To: "a@b.com", Timeout: "50ms",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = js.RunNow(cctx, JobKind, org, jobPayload{TestID: accp.ID}, nil)
	if errs.CodeOf(err) != errs.CodeDeliveryTestTimeout {
		t.Fatalf("timeout: %v", err)
	}
	got, _ := svc.Get(cctx, accp.ID)
	if got.State != StateTimedOut {
		t.Fatalf("state %s", got.State)
	}
}
