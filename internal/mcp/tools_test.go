package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/radiobros/emailbridge/internal/accounts"
	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/events"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/messages"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/submit"
	"github.com/radiobros/emailbridge/internal/tenancy"
	"github.com/radiobros/emailbridge/internal/webhook"
)

type mockAccounts struct {
	list *accounts.AccountList
	err  error
}

func (m *mockAccounts) List(context.Context, accounts.ListFilter) (*accounts.AccountList, error) {
	return m.list, m.err
}

type mockMessages struct {
	list   *messages.ListResult
	search []messages.MessageView
	get    *messages.MessageView
	err    error
}

func (m *mockMessages) ListMessages(context.Context, string, messages.ListFilter) (*messages.ListResult, error) {
	return m.list, m.err
}
func (m *mockMessages) SearchMessages(context.Context, string, messages.SearchInput) ([]messages.MessageView, error) {
	return m.search, m.err
}
func (m *mockMessages) GetMessage(context.Context, string, string, messages.GetOptions) (*messages.MessageView, error) {
	return m.get, m.err
}

type mockSubmit struct {
	res *submit.SubmitResult
	err error
}

func (m *mockSubmit) Submit(context.Context, string, submit.SubmitRequest, string, bool) (*submit.SubmitResult, error) {
	return m.res, m.err
}

type mockDeliveries struct {
	list *webhook.DeliveryList
	err  error
}

func (m *mockDeliveries) ListDeliveries(context.Context, webhook.DeliveryFilter) (*webhook.DeliveryList, error) {
	return m.list, m.err
}

type mockEvents struct {
	list *events.List
	err  error
}

func (m *mockEvents) List(context.Context, events.ListFilter) (*events.List, error) {
	return m.list, m.err
}

type mockAudit struct {
	n int
}

func (m *mockAudit) Record(context.Context, string, audit.Target, audit.Changes) error {
	m.n++
	return nil
}

func keyCtx(org string, scopes ...string) context.Context {
	ctx := store.WithOrganization(context.Background(), org)
	return tenancy.WithCaller(ctx, tenancy.Caller{
		Kind: tenancy.CallerAPIKey, APIKeyID: "key_test", OrganizationID: org, Scopes: scopes,
	})
}

func TestHasScope(t *testing.T) {
	if !hasScope([]string{"admin"}, "mcp:use") {
		t.Fatal("admin")
	}
	if !hasScope([]string{"mcp:use"}, "mcp:use") {
		t.Fatal("exact")
	}
	if !hasScope([]string{"accounts:write"}, "accounts:read") {
		t.Fatal("write→read")
	}
	if !hasScope([]string{"webhooks:read"}, "deliveries:read") {
		t.Fatal("webhook→delivery")
	}
	if !hasScope([]string{"accounts:read"}, "messages:read") {
		t.Fatal("accounts→messages")
	}
	if hasScope([]string{"mcp:use"}, "outbound:write") {
		t.Fatal("no")
	}
}

func TestToolsHappyAndErrors(t *testing.T) {
	org := store.NewID(store.PrefixOrganization)
	aud := &mockAudit{}
	accID := store.NewID(store.PrefixAccount)
	msgID := store.NewID(store.PrefixMessage)
	g := &Gateway{
		Accounts: &mockAccounts{list: &accounts.AccountList{Data: []accounts.Account{
			{ID: accID, Name: "N", Email: "a@b.co", State: "connected"},
		}}},
		Messages: &mockMessages{
			list:   &messages.ListResult{Data: []messages.MessageView{{ID: msgID, AccountID: accID, Subject: "hi", From: []messages.AddressView{{Address: "a@b.co"}}}}},
			search: []messages.MessageView{{ID: msgID, AccountID: accID, Subject: "s"}},
			get:    &messages.MessageView{ID: msgID, AccountID: accID, Text: "body a@b.co"},
		},
		Submit: &mockSubmit{res: &submit.SubmitResult{Accepted: &submit.Accepted{ID: "out_1", State: "queued"}}},
		Deliveries: &mockDeliveries{list: &webhook.DeliveryList{Data: []webhook.Delivery{
			{ID: "dlv_1", State: "succeeded", CreatedAt: time.Now()},
		}}},
		Events:     &mockEvents{list: &events.List{Data: []events.Event{{ID: "evt_1"}}}},
		Audit:      aud,
		ResolvePII: func(context.Context) (bool, int) { return true, 500 },
	}
	ctx := keyCtx(org, "mcp:use", "accounts:read", "messages:read", "outbound:write", "deliveries:read", "events:read")

	res, _, _ := g.listAccounts(ctx, nil, listAccountsIn{Limit: 10})
	if res.IsError || len(res.Content) == 0 {
		t.Fatal(res)
	}
	text := res.Content[0].(*sdkmcp.TextContent).Text
	if contains(text, "a@b.co") {
		t.Fatalf("pii: %s", text)
	}
	res, _, _ = g.listMessages(ctx, nil, listMessagesIn{AccountID: accID})
	if res.IsError {
		t.Fatal(textOf(res))
	}
	res, _, _ = g.searchMessages(ctx, nil, searchMessagesIn{AccountID: accID, Text: "x"})
	if res.IsError {
		t.Fatal(textOf(res))
	}
	res, _, _ = g.getMessage(ctx, nil, getMessageIn{AccountID: accID, MessageID: msgID})
	if res.IsError || contains(textOf(res), "a@b.co") {
		t.Fatal(textOf(res))
	}
	res, _, _ = g.sendMessage(ctx, nil, sendMessageIn{AccountID: accID, To: []string{"x@y.z"}, Subject: "s", Text: "t"})
	if res.IsError {
		t.Fatal(textOf(res))
	}
	res, _, _ = g.listDeliveries(ctx, nil, listDeliveriesIn{})
	if res.IsError {
		t.Fatal(textOf(res))
	}
	res, _, _ = g.listEvents(ctx, nil, listEventsIn{})
	if res.IsError {
		t.Fatal(textOf(res))
	}
	if aud.n < 7 {
		t.Fatalf("audit %d", aud.n)
	}

	// missing scope
	ctx2 := keyCtx(org, "mcp:use")
	res, _, _ = g.sendMessage(ctx2, nil, sendMessageIn{AccountID: accID, To: []string{"x@y.z"}})
	if !res.IsError {
		t.Fatal("want scope error")
	}
	res, _, _ = g.listAccounts(ctx2, nil, listAccountsIn{})
	if !res.IsError {
		t.Fatal("want accounts scope")
	}
	// validation
	res, _, _ = g.getMessage(ctx, nil, getMessageIn{})
	if !res.IsError {
		t.Fatal("want ids")
	}
	g.Accounts = &mockAccounts{err: errors.New("boom")}
	res, _, _ = g.listAccounts(ctx, nil, listAccountsIn{})
	if !res.IsError {
		t.Fatal("want boom")
	}
}

func TestNewServer(t *testing.T) {
	srv := NewServer(&Gateway{})
	if srv == nil {
		t.Fatal("nil")
	}
}

func TestHandlerGate(t *testing.T) {
	org := store.NewID(store.PrefixOrganization)
	h := &Handler{
		Gateway:  &Gateway{},
		Registry: features.DefaultRegistry(),
		Env:      "test",
	}
	h.server = NewServer(h.Gateway)
	h.http = sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server { return h.server }, nil)

	// unauthenticated
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/mcp", nil))
	if rr.Code != http.StatusUnauthorized && rr.Code != 401 {
		// WriteProblem may set status from errs
		if rr.Code < 400 {
			t.Fatalf("code %d body %s", rr.Code, rr.Body.String())
		}
	}

	// session user
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	ctx := tenancy.WithCaller(store.WithOrganization(req.Context(), org), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: "usr_1", OrganizationID: org, Role: tenancy.RoleOwner,
	})
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req.WithContext(ctx))
	if rr.Code < 400 {
		t.Fatalf("user: %d", rr.Code)
	}

	// api key without mcp:use
	req = httptest.NewRequest(http.MethodPost, "/mcp", nil)
	ctx = keyCtx(org, "accounts:read")
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req.WithContext(ctx))
	if rr.Code < 400 {
		t.Fatalf("scope: %d %s", rr.Code, rr.Body.String())
	}

	// mcp:use but feature off
	req = httptest.NewRequest(http.MethodPost, "/mcp", nil)
	ctx = keyCtx(org, "mcp:use")
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req.WithContext(ctx))
	if rr.Code < 400 {
		t.Fatalf("feature: %d %s", rr.Code, rr.Body.String())
	}
}

func TestResolvePIISettingsDefaults(t *testing.T) {
	r, m := ResolvePIISettings(context.Background(), nil)
	if !r || m != 4000 {
		t.Fatal(r, m)
	}
}

func TestModuleMeta(t *testing.T) {
	m := &Module{Env: "test"}
	if m.Name() != "mcp" {
		t.Fatal(m.Name())
	}
	mig := Migration(29)
	if mig.Number != 29 {
		t.Fatal(mig)
	}
	_ = m.Stop(context.Background())
}

func textOf(r *sdkmcp.CallToolResult) string {
	if r == nil || len(r.Content) == 0 {
		return ""
	}
	if t, ok := r.Content[0].(*sdkmcp.TextContent); ok {
		return t.Text
	}
	b, _ := json.Marshal(r.Content)
	return string(b)
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})())
}

func TestErrsCodeFeature(t *testing.T) {
	if errs.CodeFeatureDisabled == "" {
		t.Fatal("code")
	}
}
