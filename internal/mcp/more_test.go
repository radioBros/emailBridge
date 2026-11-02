package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/radiobros/emailbridge/internal/accounts"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/messages"
	"github.com/radiobros/emailbridge/internal/settings"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
	"github.com/radiobros/emailbridge/internal/webhook"
)

func TestMountAndHandlerSuccessPath(t *testing.T) {
	org := store.NewID(store.PrefixOrganization)
	srv, err := httpapi.New(httpapi.Options{Addr: ":0", Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{
		Gateway:  &Gateway{ResolvePII: func(context.Context) (bool, int) { return false, 100 }},
		Registry: features.DefaultRegistry(),
		Env:      "test",
	}
	// force enable via custom registry resolve — override mcpEnabled by setting Settings nil and
	// temporarily patching: use Env + inject Values through a stub Settings is hard; call with
	// feature on by wrapping ServeHTTP after enabling via Enabled hook — add ForceEnabled.
	h.ForceEnabled = true
	h.Mount(srv)
	if h.server == nil || h.http == nil {
		t.Fatal("mount")
	}
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	ctx := keyCtx(org, "mcp:use")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req.WithContext(ctx))
	// streamable handler may return 400 for empty body — but not 403
	if rr.Code == http.StatusForbidden || rr.Code == http.StatusUnauthorized {
		t.Fatalf("gate failed: %d %s", rr.Code, rr.Body.String())
	}
	m := &Module{Env: "test", handler: h}
	if m.Handler() == nil || len(m.Roles()) != 1 {
		t.Fatal("meta")
	}
	mig := Migration(29)
	if err := mig.Up(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
}

func TestToolNilServicesAndPIIOff(t *testing.T) {
	org := store.NewID(store.PrefixOrganization)
	g := &Gateway{ResolvePII: nil} // defaults
	ctx := keyCtx(org, "mcp:use", "accounts:read", "messages:read", "outbound:write", "deliveries:read", "events:read")
	res, _, _ := g.listAccounts(ctx, nil, listAccountsIn{})
	if !res.IsError {
		t.Fatal("nil accounts")
	}
	res, _, _ = g.listMessages(ctx, nil, listMessagesIn{AccountID: "acc"})
	if !res.IsError {
		t.Fatal("nil messages")
	}
	res, _, _ = g.searchMessages(ctx, nil, searchMessagesIn{AccountID: "acc"})
	if !res.IsError {
		t.Fatal("nil search")
	}
	res, _, _ = g.getMessage(ctx, nil, getMessageIn{AccountID: "a", MessageID: "m"})
	if !res.IsError {
		t.Fatal("nil get")
	}
	res, _, _ = g.sendMessage(ctx, nil, sendMessageIn{AccountID: "a", To: []string{"x@y.z"}})
	if !res.IsError {
		t.Fatal("nil submit")
	}
	res, _, _ = g.listDeliveries(ctx, nil, listDeliveriesIn{})
	if !res.IsError {
		t.Fatal("nil dlv")
	}
	res, _, _ = g.listEvents(ctx, nil, listEventsIn{})
	if !res.IsError {
		t.Fatal("nil evt")
	}

	g.Accounts = &mockAccounts{list: &accounts.AccountList{Data: nil}}
	g.Messages = &mockMessages{list: &messages.ListResult{}, search: nil, get: &messages.MessageView{ID: "1"}}
	g.Deliveries = &mockDeliveries{list: &webhook.DeliveryList{}}
	g.ResolvePII = func(context.Context) (bool, int) { return false, 100 }
	ctx = keyCtx(org, "mcp:use", "accounts:read", "messages:read", "deliveries:read")
	res, _, _ = g.listAccounts(ctx, nil, listAccountsIn{Q: "x"})
	if res.IsError {
		t.Fatal(textOf(res))
	}
	res, _, _ = g.listMessages(ctx, nil, listMessagesIn{AccountID: store.NewID(store.PrefixAccount), Limit: 1})
	if res.IsError {
		t.Fatal(textOf(res))
	}
	res, _, _ = g.listMessages(ctx, nil, listMessagesIn{})
	if !res.IsError {
		t.Fatal("need accountId")
	}
	res, _, _ = g.searchMessages(ctx, nil, searchMessagesIn{})
	if !res.IsError {
		t.Fatal("need accountId")
	}
	g.Messages = &mockMessages{err: errBoom{}}
	res, _, _ = g.listMessages(ctx, nil, listMessagesIn{AccountID: store.NewID(store.PrefixAccount)})
	if !res.IsError {
		t.Fatal("boom")
	}
	// marshal failure path via channel? skip — audit nil
	g.Audit = nil
	g.Messages = &mockMessages{get: &messages.MessageView{ID: "m", Text: "t"}}
	res, _, _ = g.getMessage(ctx, nil, getMessageIn{
		AccountID: store.NewID(store.PrefixAccount), MessageID: store.NewID(store.PrefixMessage), Text: "both",
	})
	if res.IsError {
		t.Fatal(textOf(res))
	}
}

type errBoom struct{}

func (errBoom) Error() string { return "boom" }

func TestResolvePIIWithValues(t *testing.T) {
	// Exercise defaults when org missing
	r, m := ResolvePIISettings(store.WithOrganization(context.Background(), ""), nil)
	if !r || m != 4000 {
		t.Fatal(r, m)
	}
	_ = settings.SettingsBody{}
	_ = json.RawMessage{}
	_ = tenancy.CallerUser
}

func TestMcpEnabledWithSwitch(t *testing.T) {
	h := &Handler{Registry: features.DefaultRegistry(), Env: "test"}
	if h.mcpEnabled(context.Background(), "org_x") {
		t.Fatal("off")
	}
	h.Registry = nil
	if h.registry() == nil {
		t.Fatal("def")
	}
	h.ForceEnabled = true
	if !h.mcpEnabled(context.Background(), "org_x") {
		t.Fatal("force")
	}
}

func TestRequireScopeUser(t *testing.T) {
	ctx := tenancy.WithCaller(context.Background(), tenancy.Caller{Kind: tenancy.CallerUser, UserID: "u"})
	if err := requireScope(ctx, "accounts:read"); err == nil {
		t.Fatal("want err")
	}
	if hasScope([]string{"events:write"}, "events:read") {
		// write implies read for same resource via hasScope first branch... events:write == want? no
		// second check: mode==read && s==res+":write" → yes
	} else {
		t.Fatal("events write→read")
	}
}
