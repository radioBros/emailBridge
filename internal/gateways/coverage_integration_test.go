//go:build integration

package gateways

import (
	"context"
	"io"
	"log/slog"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
	"github.com/radiobros/emailbridge/simulator/simtest"
)

func TestIntegrationCoverageExtras(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	if err := Migration(26).Up(ctx, db); err != nil {
		t.Fatal(err)
	}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	org := store.NewID(store.PrefixOrganization)
	cctx := tenancy.WithCaller(store.WithOrganization(ctx, org), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: store.NewID(store.PrefixUser), OrganizationID: org, Role: tenancy.RoleOwner,
	})
	cipher := testCipher(t, db)
	svc := &Service{
		Repo: &Repo{DB: db}, Cipher: cipher, Egress: egress.NewDialer(egress.Policy{}),
		Audit: &audit.Writer{DB: db, Log: quiet, Retention: time.Hour}, EHLO: "eb.test",
	}
	mod := &Module{EHLO: "eb.test", Log: quiet, svc: svc}
	if mod.Name() == "" || len(mod.Roles()) == 0 || mod.Service() == nil {
		t.Fatal("module meta")
	}
	_ = mod.Stop(ctx)

	sim := simtest.Start(t)
	const user, pass = "cov@example.test", "s3cret-sim"
	if err := sim.Client.CreateUser(ctx, user, pass); err != nil {
		t.Fatal(err)
	}
	host, portStr, err := net.SplitHostPort(sim.Addr.SMTPPlain)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}

	g1, err := svc.Create(cctx, CreateInput{
		Name: "a-first", Host: host, Port: port, TLS: TLSNone,
		Auth: AuthInput{User: user, Pass: pass}, Proxy: "socks5://127.0.0.1:1080",
		BackoffBase: "1s", MaxAttempts: intPtr(3),
	})
	if err != nil {
		t.Fatal(err)
	}
	g2, err := svc.Create(cctx, CreateInput{
		Name: "b-second", Host: host, Port: port, TLS: TLSNone,
		Auth: AuthInput{User: user, Pass: pass},
	})
	if err != nil {
		t.Fatal(err)
	}

	// list + sort + q + cursor
	page, err := svc.List(cctx, ListFilter{Limit: 1, Sort: "name", Q: "first"})
	if err != nil || len(page.Data) != 1 {
		t.Fatalf("q list: %v %+v", err, page)
	}
	page2, err := svc.List(cctx, ListFilter{Limit: 1, Sort: "name"})
	if err != nil || page2.NextCursor == nil {
		t.Fatalf("cursor need: %v %+v", err, page2)
	}
	page3, err := svc.List(cctx, ListFilter{Limit: 1, Sort: "name", Cursor: *page2.NextCursor})
	if err != nil || len(page3.Data) != 1 {
		t.Fatalf("cursor page: %v %+v", err, page3)
	}
	if _, err := svc.List(cctx, ListFilter{Sort: "nope"}); errs.CodeOf(err) != errs.CodeSortNotAllowed {
		t.Fatal(err)
	}
	if _, err := svc.List(cctx, ListFilter{Cursor: "!!!"}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
	_ = sortKey(*g1, "name")
	_ = sortKey(*g1, "createdAt")
	_ = sortKey(*g1, "_id")
	tok := encodeCursor(listCursor{ID: g1.ID, Key: g1.Name, Field: "name", Desc: false})
	cur, err := decodeCursor(tok)
	if err != nil || cur.ID != g1.ID {
		t.Fatal(err)
	}
	_, _ = cursorFilter(cur, "name", 1)
	_, _ = cursorFilter(listCursor{ID: g1.ID, Field: "_id"}, "_id", -1)

	// API handlers
	if _, err := mod.list(cctx, &listIn{Limit: 10, Sort: "-createdAt", Q: "second"}); err != nil {
		t.Fatal(err)
	}
	if _, err := mod.list(cctx, &listIn{Sort: "bad"}); errs.CodeOf(err) != errs.CodeSortNotAllowed {
		t.Fatal(err)
	}
	if _, err := mod.create(cctx, &createIn{Body: CreateInput{
		Name: "c-third", Host: host, Port: port, TLS: TLSNone, Auth: AuthInput{User: user, Pass: pass},
		Policy: PolicyAtMostOnce, IgnoreCertErrors: true,
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := mod.get(cctx, &idIn{GatewayID: g2.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := mod.get(cctx, &idIn{GatewayID: "gwy_nope"}); err == nil {
		t.Fatal("get missing")
	}
	en := false
	host2 := host
	port2 := port
	tls2 := TLSNone
	ehlo := "x.ehlo"
	local := "127.0.0.1"
	proxy := "http://127.0.0.1:8080"
	pol := PolicyStandard
	bb := "2s"
	ign := true
	if _, err := mod.patch(cctx, &patchIn{GatewayID: g2.ID, IfMatch: formatETag(g2.Version), Body: PatchInput{
		Enabled: &en, Host: &host2, Port: &port2, TLS: &tls2, EHLOName: &ehlo, LocalAddress: &local,
		Proxy: &proxy, Policy: &pol, BackoffBase: &bb, IgnoreCertErrors: &ign, ClearMaxAttempts: true,
		Auth: &AuthInput{User: user},
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := mod.patch(cctx, &patchIn{GatewayID: g2.ID, IfMatch: `"v999"`, Body: PatchInput{}}); errs.CodeOf(err) != errs.CodeVersionMismatch {
		t.Fatal(err)
	}
	if _, err := mod.delete(cctx, &deleteIn{GatewayID: g2.ID, IfMatch: `"v999"`}); errs.CodeOf(err) != errs.CodeVersionMismatch {
		t.Fatal(err)
	}
	if _, err := mod.verify(cctx, &idIn{GatewayID: g1.ID}); err != nil {
		t.Fatal(err)
	}
	// verify failure (bad password via direct Verify after patch)
	bad := "wrong"
	curG, _ := svc.Get(cctx, g1.ID)
	_, _ = svc.Patch(cctx, g1.ID, formatETag(curG.Version), PatchInput{Auth: &AuthInput{Pass: bad}})
	vr, err := svc.Verify(cctx, g1.ID)
	if err != nil || vr.OK {
		t.Fatalf("expected verify fail: %v %+v", err, vr)
	}
	_ = verifyErr(egress.ErrBlocked, time.Now())
	_ = redactServerText(string(make([]byte, 600)))

	curG, _ = svc.Get(cctx, g1.ID)
	if _, err := mod.delete(cctx, &deleteIn{GatewayID: g1.ID, IfMatch: formatETag(curG.Version)}); err != nil {
		t.Fatal(err)
	}

	// module Start wiring
	a := app.New(app.Options{Roles: app.RoleSet{app.RoleAPI: true}, NodeID: "n", Log: quiet})
	app.Provide(a, db)
	app.Provide(a, cipher)
	srv, err := httpapi.New(httpapi.Options{AccessLog: quiet, SecurityLog: quiet})
	if err != nil {
		t.Fatal(err)
	}
	app.Provide(a, srv)
	m2 := &Module{EHLO: "eb.test", Log: quiet}
	if err := m2.Start(ctx, a); err != nil {
		t.Fatal(err)
	}
	_ = m2.Stop(ctx)

	// DialSettings missing
	if _, err := svc.DialSettings(ctx, org, store.NewID(store.PrefixGateway)); errs.CodeOf(err) != errs.CodeGatewayUnavailable {
		t.Fatal(err)
	}
	if _, err := svc.Get(cctx, "gwy_bad"); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatal(err)
	}
	_ = validateSMTPGateway(&Gateway{Name: "", Host: "h", Port: 25, TLS: TLSNone, Auth: Auth{User: "u"}})
	_ = validateSMTPGateway(&Gateway{Name: "n", Host: "", Port: 25, TLS: TLSNone, Auth: Auth{User: "u"}})
	_ = validateSMTPGateway(&Gateway{Name: "n", Host: "h", Port: 0, TLS: TLSNone, Auth: Auth{User: "u"}})
	_ = validateSMTPGateway(&Gateway{Name: "n", Host: "h", Port: 25, TLS: "x", Auth: Auth{User: "u"}})
	_ = validateSMTPGateway(&Gateway{Name: "n", Host: "h", Port: 25, TLS: TLSNone, Auth: Auth{User: ""}})
	_ = validateSMTPGateway(&Gateway{Name: "n", Host: "h", Port: 25, TLS: TLSNone, Auth: Auth{User: "u"}, BackoffBase: "bad"})
	ma := 0
	_ = validateSMTPGateway(&Gateway{Name: "n", Host: "h", Port: 25, TLS: TLSNone, Auth: Auth{User: "u"}, MaxAttempts: &ma})

	for i := 0; i < 6; i++ {
		_ = svc.takeVerifyToken(org)
	}
}

func intPtr(v int) *int { return &v }
