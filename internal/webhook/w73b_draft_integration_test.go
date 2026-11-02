//go:build integration

package webhook

import (
	"testing"

	"github.com/radiobros/emailbridge/internal/rules"
)

// TestIntegrationW73bDraftEndpointRuleTest: the admin editor evaluates an unsaved filter / map on an endpoint
// that has no stored rule yet, and a draft on an endpoint with a rule does not change the stored rule.
func TestIntegrationW73bDraftEndpointRuleTest(t *testing.T) {
	h := newWebhookHarness(t)
	ctx := h.ctx
	m := &Module{service: h.service}
	ep, err := h.service.CreateEndpoint(ctx, EndpointCreate{
		Name: "draft", URL: h.w73Receiver(t), Events: []string{"*"}, Secret: testSecret(81),
	})
	if err != nil {
		t.Fatal(err)
	}
	filter, mapExpr := w73InboundFilter, `{"event": "messageNew", "subject": data.subject}`
	inbox := map[string]any{"type": "message.received", "data": map[string]any{
		"mailbox": map[string]any{"path": "INBOX"}, "subject": "hello"}}
	res, err := m.testEndpointRule(ctx, &endpointRuleTestInput{EndpointID: ep.ID, Body: rules.RouteTestRequest{
		Event: inbox, Filter: &filter, Map: &mapExpr,
	}})
	if err != nil || !res.Body.Matched || res.Body.Mapped.(map[string]any)["subject"] != "hello" {
		t.Fatalf("draft on an endpoint without a rule = %+v %v", res, err)
	}
	other := map[string]any{"type": "message.received", "data": map[string]any{"mailbox": map[string]any{"path": "Archive"}}}
	res, err = m.testEndpointRule(ctx, &endpointRuleTestInput{EndpointID: ep.ID, Body: rules.RouteTestRequest{
		Event: other, Filter: &filter,
	}})
	if err != nil || res.Body.Matched || len(res.Body.Trace) != 2 || res.Body.Trace[1].Result {
		t.Fatalf("draft NO MATCH with trace = %+v %v", res, err)
	}
	broken := `type == `
	res, err = m.testEndpointRule(ctx, &endpointRuleTestInput{EndpointID: ep.ID, Body: rules.RouteTestRequest{
		Event: inbox, Filter: &broken,
	}})
	if err != nil || res.Body.Error == "" || res.Body.ErrorAt == nil || res.Body.ErrorAt.Path != "filter" {
		t.Fatalf("draft compile error = %+v %v", res, err)
	}

	// Store a rule, then try a draft that contradicts it: the stored rule still answers a plain test.
	saved, err := h.service.UpdateEndpoint(ctx, ep.ID, EndpointUpdate{Rule: &EndpointRule{Filter: filter}}, formatETag(ep.Version))
	if err != nil {
		t.Fatal(err)
	}
	never := "false"
	if res, err = m.testEndpointRule(ctx, &endpointRuleTestInput{EndpointID: saved.ID, Body: rules.RouteTestRequest{
		Event: inbox, Filter: &never,
	}}); err != nil || res.Body.Matched {
		t.Fatalf("draft false = %+v %v", res, err)
	}
	if res, err = m.testEndpointRule(ctx, &endpointRuleTestInput{EndpointID: saved.ID, Body: rules.RouteTestRequest{
		Event: inbox,
	}}); err != nil || !res.Body.Matched {
		t.Fatalf("stored rule after a draft = %+v %v", res, err)
	}
}
