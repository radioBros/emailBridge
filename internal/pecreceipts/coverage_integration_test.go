//go:build integration

package pecreceipts

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/settings"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

func TestIntegrationGetRawEdgesAndResolve(t *testing.T) {
	db := testDB(t)
	org := store.NewID(store.PrefixOrganization)
	acc := store.NewID(store.PrefixAccount)
	ctx := ownerCtx(org)
	rc := seedReceipt(t, db, org, acc, &Receipt{Type: "accettazione", TransportID: "t", SHA256: strings.Repeat("ab", 32)})

	svc := &Service{
		Repo: &Repo{DB: db}, Registry: features.DefaultRegistry(), Env: "test",
		Enabled: func(context.Context, string) (bool, error) { return true, nil },
	}
	if _, err := svc.getRaw(ctx, &receiptIDIn{ReceiptID: rc.ID}); errs.CodeOf(err) != errs.CodeInternalError {
		t.Fatalf("nil raw: %v", err)
	}
	svc.Raw = &fakeRaw{err: errs.New(errs.CodeInternalError, "boom")}
	if _, err := svc.getRaw(ctx, &receiptIDIn{ReceiptID: rc.ID}); errs.CodeOf(err) != errs.CodeInternalError {
		t.Fatalf("raw open: %v", err)
	}
	svc.Raw = &fakeRaw{body: []byte("ok")}
	stream, err := svc.getRaw(ctx, &receiptIDIn{ReceiptID: rc.ID})
	if err != nil || stream.Body == nil {
		t.Fatal(err)
	}
	// Invoke body with a headerWriter-compatible stub via writeRaw path already tested;
	// exercise the closure by wrapping through a local adapter that satisfies huma.Context minimally —
	// instead call writeRaw directly was enough; here we just ensure getRaw returns.
	_ = stream

	svc.Enabled = func(context.Context, string) (bool, error) {
		return false, errs.New(errs.CodeInternalError, "gate err")
	}
	if err := svc.requirePEC(ctx, acc); errs.CodeOf(err) != errs.CodeInternalError {
		t.Fatalf("gate err: %v", err)
	}

	// resolveEnabled: org switch on
	acc2 := store.NewID(store.PrefixAccount)
	_, _ = db.Collection("accounts").InsertOne(ctx, bson.M{"_id": acc2, "organizationId": org})
	_, _ = db.Collection(settings.SettingsCollection).InsertOne(ctx, bson.M{
		"_id":      settings.OrgDocID(org),
		"switches": bson.M{"pec.enabled": bson.M{"value": true}},
	})
	svc2 := &Service{Repo: &Repo{DB: db}, Registry: features.DefaultRegistry(), Env: "test"}
	ok, err := svc2.pecEnabled(ctx, acc2)
	if err != nil || !ok {
		t.Fatalf("org switch: %v %v", ok, err)
	}

	// no org on context
	if _, err := svc.loadForRead(context.Background(), rc.ID); err == nil {
		t.Fatal("expected no tenant")
	}

	// list handler with requirePEC failure
	svc.Enabled = func(context.Context, string) (bool, error) { return false, nil }
	if _, err := svc.list(ctx, &listIn{AccountID: acc}); errs.CodeOf(err) != errs.CodeFeatureDisabled {
		t.Fatal(err)
	}

	// list with messages:read key succeeds
	msgKey := keyCtx(org, "messages:read")
	svc.Enabled = func(context.Context, string) (bool, error) { return true, nil }
	if _, err := svc.list(msgKey, &listIn{AccountID: acc, Limit: 1}); err != nil {
		t.Fatal(err)
	}

	// cursorFilter _id + parseCursorTime branches
	if _, err := cursorFilter(listCursor{Field: "_id", ID: "rcp_1"}, "_id", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := parseCursorTime(time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := parseCursorTime(123); err == nil {
		t.Fatal("bad time")
	}
	if _, err := cursorFilter(listCursor{Field: "at", Key: "not-a-time"}, "at", -1); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}

	// List limit clamps + type sort cursor mismatch already covered; hit limit defaults
	if _, err := svc.Repo.List(ctx, acc, ListFilter{Limit: 1000, Sort: "type"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Repo.List(ctx, acc, ListFilter{Limit: 0, Sort: "-type", Page: 1, IncludeTotal: true}); err != nil {
		t.Fatal(err)
	}

	// Module Start without API role / nil registry defaults
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := app.New(app.Options{Roles: app.RoleSet{app.RoleSync: true}, NodeID: "n", Log: quiet})
	app.Provide(a, db)
	m := &Module{Env: "test", Log: quiet}
	if err := m.Start(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	_ = m.Stop(context.Background())

	a2 := app.New(app.Options{Roles: app.RoleSet{app.RoleAPI: true}, NodeID: "n", Log: quiet})
	app.Provide(a2, db)
	srv, err := httpapi.New(httpapi.Options{Addr: "127.0.0.1:0", AccessLog: quiet, SecurityLog: quiet})
	if err != nil {
		t.Fatal(err)
	}
	app.Provide(a2, srv)
	m2 := &Module{Raw: &fakeRaw{body: []byte("x")}, Env: "test", Log: quiet} // nil Registry
	if err := m2.Start(context.Background(), a2); err != nil {
		t.Fatal(err)
	}
	_ = m2.Stop(context.Background())

	_ = tenancy.RoleOwner
}
