package mcp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/radiobros/emailbridge/internal/events"
	"github.com/radiobros/emailbridge/internal/features"
	"github.com/radiobros/emailbridge/internal/messages"
	"github.com/radiobros/emailbridge/internal/settings"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/submit"
	"github.com/radiobros/emailbridge/internal/webhook"
)

type fakeEff struct {
	body settings.SettingsBody
	err  error
}

func (f fakeEff) Effective(context.Context, string) (settings.SettingsBody, error) {
	return f.body, f.err
}

func TestResolvePIIFull(t *testing.T) {
	org := store.NewID(store.PrefixOrganization)
	ctx := store.WithOrganization(context.Background(), org)
	r, m := ResolvePIISettings(ctx, fakeEff{body: settings.SettingsBody{
		Values: map[string]settings.SettingValue{
			"mcp.piiRedact":    {Value: false},
			"mcp.bodyMaxChars": {Value: 100},
		},
	}})
	if r || m != 100 {
		t.Fatal(r, m)
	}
	_, m = ResolvePIISettings(ctx, fakeEff{body: settings.SettingsBody{
		Values: map[string]settings.SettingValue{"mcp.bodyMaxChars": {Value: int64(200)}},
	}})
	if m != 200 {
		t.Fatal(m)
	}
	_, m = ResolvePIISettings(ctx, fakeEff{body: settings.SettingsBody{
		Values: map[string]settings.SettingValue{"mcp.bodyMaxChars": {Value: float64(300)}},
	}})
	if m != 300 {
		t.Fatal(m)
	}
	r, m = ResolvePIISettings(ctx, fakeEff{err: errors.New("x")})
	if !r || m != 4000 {
		t.Fatal(r, m)
	}
}

func TestMcpEnabledSettingsOn(t *testing.T) {
	h := &Handler{
		Registry: features.DefaultRegistry(), Env: "test",
		Settings: fakeEff{body: settings.SettingsBody{
			Switches: map[string]settings.SettingValue{switchMCP: {Value: true}},
		}},
	}
	if !h.mcpEnabled(context.Background(), "org") {
		t.Fatal("want on")
	}
	h.Settings = fakeEff{body: settings.SettingsBody{
		Switches: map[string]settings.SettingValue{switchMCP: {Value: false}},
	}}
	if h.mcpEnabled(context.Background(), "org") {
		t.Fatal("want off")
	}
}

func TestHandlerEnabledPass(t *testing.T) {
	org := store.NewID(store.PrefixOrganization)
	h := &Handler{Gateway: &Gateway{}, Registry: features.DefaultRegistry(), Env: "test", ForceEnabled: true}
	h.server = NewServer(h.Gateway)
	h.http = sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server { return h.server }, nil)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	h.ServeHTTP(rr, req.WithContext(keyCtx(org, "mcp:use")))
	if rr.Code == http.StatusForbidden || rr.Code == http.StatusUnauthorized {
		t.Fatalf("%d %s", rr.Code, rr.Body.String())
	}
}

func TestDeliveryEventsErrors(t *testing.T) {
	org := store.NewID(store.PrefixOrganization)
	ctx := keyCtx(org, "mcp:use", "deliveries:read", "events:read", "outbound:write", "messages:read")
	g := &Gateway{
		Deliveries: &mockDeliveries{err: errors.New("d")},
		Events:     &mockEvents{err: errors.New("e")},
		Submit:     &mockSubmit{err: errors.New("s")},
		Messages:   &mockMessages{err: errors.New("m")},
		ResolvePII: func(context.Context) (bool, int) { return true, 50 },
	}
	res, _, _ := g.listDeliveries(ctx, nil, listDeliveriesIn{Limit: 1})
	if !res.IsError {
		t.Fatal("dlv")
	}
	res, _, _ = g.listEvents(ctx, nil, listEventsIn{Limit: 1, Types: []string{"message.received"}})
	if !res.IsError {
		t.Fatal("evt")
	}
	res, _, _ = g.sendMessage(ctx, nil, sendMessageIn{AccountID: store.NewID(store.PrefixAccount), To: []string{"a@b.co"}})
	if !res.IsError {
		t.Fatal("send")
	}
	res, _, _ = g.searchMessages(ctx, nil, searchMessagesIn{AccountID: store.NewID(store.PrefixAccount)})
	if !res.IsError {
		t.Fatal("search")
	}
	g.Deliveries = &mockDeliveries{list: &webhook.DeliveryList{Data: []webhook.Delivery{{ID: "dlv_1"}}}}
	g.Events = &mockEvents{list: &events.List{Data: []events.Event{{ID: "evt_1"}}}}
	g.Submit = &mockSubmit{res: &submit.SubmitResult{Accepted: &submit.Accepted{ID: "out_1", State: "queued"}}}
	g.Messages = &mockMessages{search: []messages.MessageView{{ID: "msg_1"}}, get: &messages.MessageView{ID: "msg_1"}}
	res, _, _ = g.listDeliveries(ctx, nil, listDeliveriesIn{})
	if res.IsError {
		t.Fatal(textOf(res))
	}
	res, _, _ = g.listEvents(ctx, nil, listEventsIn{})
	if res.IsError {
		t.Fatal(textOf(res))
	}
	// RedactJSON invalid then array
	_ = RedactJSON(`[1,2,"a@b.co"]`, 10)
}
