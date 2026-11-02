//go:build integration

package webhook

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/events"
	eventtypes "github.com/radiobros/emailbridge/internal/events/types"
	"github.com/radiobros/emailbridge/internal/jobs"
	"github.com/radiobros/emailbridge/internal/rules"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

const (
	w73InboundFilter = `type == "message.received" && isInbox()`
	w73InboundMap    = `{"key": env.webhookKey, "body": {"id": data.messageId, "date": event.time,
 "account": account.externalId != "" ? account.externalId : account.id, "event": "messageNew",
 "headers": data.headers, "emailid": data.messageIdHeader, "subject": data.subject}}`
	w73FakeKey = "fake-key-integration"
)

func (h *webhookHarness) w73Event(t *testing.T, org, accountID, path, subject string, labels []string, at time.Time) events.Event {
	t.Helper()
	ctx := store.WithOrganization(context.Background(), org)
	got, err := h.service.Events.Emit(ctx, events.Draft{
		Type: events.MessageReceived, OrganizationID: org, AccountID: accountID,
		Subject: store.NewID(store.PrefixMessage), Time: at,
		Data: &eventtypes.MessageReceived{
			AccountRef: eventtypes.AccountRef{AccountID: accountID},
			MessageID:  store.NewID(store.PrefixMessage), MessageIDHeader: "<" + subject + "@example.com>",
			Mailbox: eventtypes.Mailbox{MailboxID: store.NewID(store.PrefixMailbox), Path: path},
			Subject: subject, Labels: labels,
			Headers:      map[string][]string{"subject": {subject}, "message-id": {"<" + subject + "@example.com>"}},
			InternalDate: at,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return got[0]
}

func (h *webhookHarness) w73Deliveries(t *testing.T, org, eventID string) []Delivery {
	t.Helper()
	cur, err := h.db.Collection(DeliveriesCollection).Find(context.Background(), bson.M{"organizationId": org, "eventId": eventID})
	if err != nil {
		t.Fatal(err)
	}
	var rows []Delivery
	if err := cur.All(context.Background(), &rows); err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestIntegrationW73EndpointRuleRouting(t *testing.T) {
	h := newWebhookHarness(t)
	ctx := h.ctx
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := rules.W73Migration(45).Up(ctx, h.db); err != nil {
		t.Fatal(err)
	}
	stats := rules.NewStatsRecorder(h.db)
	stats.Log = quiet
	h.rules.Stats = stats
	h.rules.Lists = &rules.ListCache{DB: h.db}
	h.rules.AccountLoader = func(_ context.Context, _, id string) (map[string]any, error) {
		return map[string]any{"id": id, "externalId": "studio-rossi"}, nil
	}
	h.rules.EnvLoader = func(context.Context, string) (map[string]string, []string, error) {
		return map[string]string{"webhookKey": w73FakeKey}, []string{"webhookKey"}, nil
	}
	accountID := store.NewID(store.PrefixAccount)

	ep, err := h.service.CreateEndpoint(ctx, EndpointCreate{
		Name: "legacyapp-inbound", URL: h.w73Receiver(t), Events: []string{"*"},
		Secret: testSecret(61), Rule: &EndpointRule{Filter: w73InboundFilter, Map: w73InboundMap, CurrentVersion: 99},
	})
	if err != nil {
		t.Fatal(err)
	}
	if ep.Rule == nil || ep.Rule.CurrentVersion != 1 {
		t.Fatalf("client currentVersion must be ignored: %+v", ep.Rule)
	}

	// Inbox (IMAP path) and Gmail All Mail with the \Inbox label are delivered with the mapped body;
	// another folder is skipped.
	now := time.Now().UTC().Truncate(time.Second)
	inbox := h.w73Event(t, h.orgID, accountID, "INBOX", "in-1", nil, now)
	gmail := h.w73Event(t, h.orgID, accountID, "[Gmail]/All Mail", "in-2", []string{`\Inbox`}, now)
	other := h.w73Event(t, h.orgID, accountID, "Archive", "in-3", nil, now)
	for _, ev := range []events.Event{inbox, gmail, other} {
		if err := h.service.RouteEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}
	for _, ev := range []events.Event{inbox, gmail} {
		rows := h.w73Deliveries(t, h.orgID, ev.ID)
		if len(rows) != 1 || rows[0].RouteID != "" || rows[0].MappedBody != "" || rows[0].MappedBodyEnc.IsZero() {
			t.Fatalf("deliveries for %s = %+v", ev.ID, rows)
		}
		mappedBody, ok, err := h.service.openMappedBody(ctx, &rows[0])
		if err != nil || !ok {
			t.Fatalf("open mapped body: %v", err)
		}
		var body map[string]any
		if err := json.Unmarshal([]byte(mappedBody), &body); err != nil {
			t.Fatal(err)
		}
		inner, _ := body["body"].(map[string]any)
		hdrs, _ := inner["headers"].(map[string]any)
		if body["key"] != w73FakeKey || inner["account"] != "studio-rossi" || inner["event"] != "messageNew" ||
			inner["emailid"] == "" || hdrs["subject"] == nil || inner["date"] != now.Format(time.RFC3339) {
			t.Fatalf("mapped body = %s", mappedBody)
		}
	}
	if rows := h.w73Deliveries(t, h.orgID, other.ID); len(rows) != 0 {
		t.Fatalf("other folder delivered: %+v", rows)
	}

	// The rules.enabled switch governs routes only: the endpoint filter still applies (never fails open).
	h.service.RulesEnabled = func(context.Context, string) bool { return false }
	off := h.w73Event(t, h.orgID, accountID, "Sent", "off-1", nil, now)
	if err := h.service.RouteEvent(ctx, off); err != nil {
		t.Fatal(err)
	}
	if rows := h.w73Deliveries(t, h.orgID, off.ID); len(rows) != 0 {
		t.Fatalf("rules off delivered unfiltered: %+v", rows)
	}
	h.service.RulesEnabled = nil
	// No engine at all: an endpoint with a rule is skipped (fail closed).
	engine := h.service.Engine
	h.service.Engine = nil
	noEngine := h.w73Event(t, h.orgID, accountID, "INBOX", "ne-1", nil, now)
	if err := h.service.RouteEvent(ctx, noEngine); err != nil {
		t.Fatal(err)
	}
	if rows := h.w73Deliveries(t, h.orgID, noEngine.ID); len(rows) != 0 {
		t.Fatalf("no engine delivered: %+v", rows)
	}
	h.service.Engine = engine

	// No double delivery: a matching route targeting the rule-mode endpoint owns the delivery.
	route, err := h.rules.Create(ctx, rules.RouteCreate{
		Name: "vip-route", EndpointID: ep.ID, Filter: `subject().startsWith("vip")`, Map: `{"route": true}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	vip := h.w73Event(t, h.orgID, accountID, "INBOX", "vip-1", nil, now)
	plain := h.w73Event(t, h.orgID, accountID, "INBOX", "plain-1", nil, now)
	for _, ev := range []events.Event{vip, plain} {
		if err := h.service.RouteEvent(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}
	if rows := h.w73Deliveries(t, h.orgID, vip.ID); len(rows) != 1 || rows[0].RouteID != route.ID {
		t.Fatalf("vip deliveries = %+v", rows)
	}
	if rows := h.w73Deliveries(t, h.orgID, plain.ID); len(rows) != 1 || rows[0].RouteID != "" {
		t.Fatalf("plain deliveries = %+v", rows)
	}

	// Counters + decisions per endpoint rule, never visible to another organisation.
	st, err := h.service.EndpointRuleStats(ctx, ep.ID)
	if err != nil {
		t.Fatal(err)
	}
	// inbox, gmail, plain matched; Archive + Sent skipped; no-engine is an error; vip went to the route.
	if st.Evaluated != 6 || st.Matched != 3 || st.Skipped != 2 || st.Errors != 1 || st.LastMatchedAt == nil {
		t.Fatalf("stats = %+v", st)
	}
	dec, err := h.service.EndpointRuleDecisions(ctx, ep.ID, 10)
	if err != nil || len(dec.Data) == 0 {
		t.Fatalf("decisions = %+v %v", dec, err)
	}
	otherOrg := h.caller(store.NewID(store.PrefixOrganization), tenancy.RoleOwner)
	if _, err := h.service.EndpointRuleStats(otherOrg, ep.ID); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatalf("cross-tenant stats: %v", err)
	}
	if _, err := h.service.EndpointRuleDecisions(otherOrg, ep.ID, 10); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatalf("cross-tenant decisions: %v", err)
	}
	if foreign, err := h.rules.GetStats(otherOrg, "endpoint", ep.ID); err != nil || foreign.Evaluated != 0 {
		t.Fatalf("foreign org sees counters: %+v %v", foreign, err)
	}
	if foreign, err := h.rules.ListDecisions(otherOrg, "endpoint", ep.ID, 10); err != nil || len(foreign.Data) != 0 {
		t.Fatalf("foreign org sees decisions: %+v %v", foreign, err)
	}

	// /rule/test with a sample and with a stored event; the secret key is masked in test output.
	res, err := h.service.TestEndpointRule(ctx, ep.ID, rules.RouteTestRequest{Sample: events.MessageReceived})
	if err != nil || !res.Matched || len(res.Trace) != 2 {
		t.Fatalf("test sample = %+v %v", res, err)
	}
	if b, _ := json.Marshal(res); strings.Contains(string(b), w73FakeKey) || res.Mapped == nil {
		t.Fatalf("secret leaked in test output (or no mapped): %s", b)
	}
	stored, err := h.service.TestEndpointRule(ctx, ep.ID, rules.RouteTestRequest{EventID: gmail.ID})
	if err == nil && !stored.Matched {
		t.Fatalf("test stored gmail event = %+v", stored)
	}
}

func TestIntegrationW73EndpointRuleVersionsAndDryRun(t *testing.T) {
	h := newWebhookHarness(t)
	ctx := h.ctx
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, m := range []store.Migration{rules.W73Migration(45), jobs.Migration(2)} {
		if err := m.Up(ctx, h.db); err != nil {
			t.Fatal(err)
		}
	}
	js := &jobs.Service{DB: h.db, NodeID: "n1", Log: quiet}
	h.rules.Jobs = js
	h.rules.Lists = &rules.ListCache{DB: h.db}
	h.rules.RegisterHandlers()

	ep, err := h.service.CreateEndpoint(ctx, EndpointCreate{
		Name: "versions", URL: h.w73Receiver(t), Events: []string{events.MessageReceived},
		Secret: testSecret(62), Rule: &EndpointRule{Filter: `isInbox()`},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.service.CreateEndpoint(ctx, EndpointCreate{
		Name: "bad-builder", URL: h.w73Receiver(t), Events: []string{"*"}, Secret: testSecret(63),
		Rule: &EndpointRule{Builder: map[string]any{"match": "all", "conditions": []any{map[string]any{"field": "nope", "op": "eq", "value": 1}}}},
	}); err == nil {
		t.Fatal("invalid builder accepted")
	}
	if _, err := h.service.CreateEndpoint(ctx, EndpointCreate{
		Name: "bad-cel", URL: h.w73Receiver(t), Events: []string{"*"}, Secret: testSecret(64),
		Rule: &EndpointRule{Filter: `isInbox(`},
	}); errs.CodeOf(err) != errs.CodeRouteCompileError {
		t.Fatalf("bad cel: %v", err)
	}
	ep, err = h.service.UpdateEndpoint(ctx, ep.ID, EndpointUpdate{Rule: &EndpointRule{Filter: `isInbox() && hasAttachments()`}}, formatETag(ep.Version))
	if err != nil || ep.Rule.CurrentVersion != 2 {
		t.Fatalf("update rule: %+v %v", ep, err)
	}
	// Same content again: no new version.
	ep, err = h.service.UpdateEndpoint(ctx, ep.ID, EndpointUpdate{Rule: &EndpointRule{Filter: `isInbox() && hasAttachments()`}}, formatETag(ep.Version))
	if err != nil || ep.Rule.CurrentVersion != 2 {
		t.Fatalf("same rule bumped version: %+v %v", ep.Rule, err)
	}
	list, err := h.service.ListEndpointRuleVersions(ctx, ep.ID, 0, 0, true)
	if err != nil || len(list.Data) != 2 || list.Data[0].Version != 2 || list.Data[1].Filter != `isInbox()` || list.Total == nil || *list.Total != 2 {
		t.Fatalf("versions = %+v %v", list, err)
	}
	if _, err := h.service.RestoreEndpointRuleVersion(ctx, ep.ID, 1, `"v1"`); errs.CodeOf(err) != errs.CodeVersionMismatch {
		t.Fatalf("stale If-Match: %v", err)
	}
	if _, err := h.service.RestoreEndpointRuleVersion(ctx, ep.ID, 9, ""); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatalf("missing version: %v", err)
	}
	if _, err := h.service.RestoreEndpointRuleVersion(ctx, ep.ID, 0, ""); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("version 0: %v", err)
	}
	restored, err := h.service.RestoreEndpointRuleVersion(ctx, ep.ID, 1, formatETag(ep.Version))
	if err != nil || restored.Rule.CurrentVersion != 3 || restored.Rule.Filter != `isInbox()` {
		t.Fatalf("restore = %+v %v", restored, err)
	}
	otherOrg := h.caller(store.NewID(store.PrefixOrganization), tenancy.RoleOwner)
	if _, err := h.service.ListEndpointRuleVersions(otherOrg, ep.ID, 0, 0, false); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatalf("cross-tenant versions: %v", err)
	}
	if _, err := h.service.RestoreEndpointRuleVersion(otherOrg, ep.ID, 1, ""); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatalf("cross-tenant restore: %v", err)
	}
	if n, err := h.db.Collection(audit.Collection).CountDocuments(ctx, bson.M{"action": "endpoint.rule.restore"}); err != nil || n != 1 {
		t.Fatalf("restore audit rows = %d %v", n, err)
	}
	// Clearing the rule drops it (no rule mode any more).
	cleared, err := h.service.UpdateEndpoint(ctx, ep.ID, EndpointUpdate{Rule: &EndpointRule{}}, formatETag(restored.Version))
	if err != nil || cleared.Rule != nil {
		t.Fatalf("clear rule: %+v %v", cleared.Rule, err)
	}

	// Dry-run: stored rule missing → a draft is required; with a draft it scans this org's events only.
	if _, err := h.service.DryRunEndpointRule(ctx, ep.ID, rules.DryRunRequest{}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("dry-run without rule: %v", err)
	}
	accountID := store.NewID(store.PrefixAccount)
	now := time.Now().UTC()
	h.w73Event(t, h.orgID, accountID, "INBOX", "dry-in", nil, now)
	h.w73Event(t, h.orgID, accountID, "Sent", "dry-sent", nil, now)
	foreignOrg := store.NewID(store.PrefixOrganization)
	h.w73Event(t, foreignOrg, accountID, "INBOX", "dry-foreign", nil, now)
	job, err := h.service.DryRunEndpointRule(ctx, ep.ID, rules.DryRunRequest{Filter: `isInbox()`, Map: `{"s": subject()}`})
	if err != nil || job.Kind != rules.JobKindDryRun {
		t.Fatalf("dry-run job = %+v %v", job, err)
	}
	stored, err := js.Get(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(stored.Payload, &payload); err != nil || payload["endpointId"] != ep.ID {
		t.Fatalf("payload = %s %v", stored.Payload, err)
	}
	out, err := js.RunNow(ctx, rules.JobKindDryRun, h.orgID, stored.Payload, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, _ := out.(rules.DryRunResult)
	if res.Scanned != 2 || res.Matched != 1 || len(res.Samples) != 1 {
		t.Fatalf("dry-run result = %+v", out)
	}
	if _, err := h.service.DryRunEndpointRule(otherOrg, ep.ID, rules.DryRunRequest{Filter: "true"}); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatalf("cross-tenant dry-run: %v", err)
	}
}

func TestIntegrationW73NeedsFromEndpointRules(t *testing.T) {
	h := newWebhookHarness(t)
	org := store.NewID(store.PrefixOrganization)
	_, err := h.db.Collection(EndpointsCollection).InsertMany(context.Background(), []any{
		bson.M{"_id": "w1", "name": "w1", "organizationId": org, "enabled": true, "payload": bson.M{},
			"rule": bson.M{"filter": `type == "message.received" && isInbox()`, "map": `{"headers": data.headers}`}},
		bson.M{"_id": "w2", "name": "w2", "organizationId": org, "enabled": true, "payload": bson.M{},
			"rule": bson.M{"filter": `data.text.plain.contains("x") && size(data.attachments) > 0`}},
	})
	if err != nil {
		t.Fatal(err)
	}
	n, err := (&NeedsResolver{DB: h.db}).Needs(context.Background(), org)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(n.Headers, []string{"*"}) || !n.Text || !n.Attachments {
		t.Fatalf("needs = %+v", n)
	}
	single := store.NewID(store.PrefixOrganization)
	if _, err := h.db.Collection(EndpointsCollection).InsertOne(context.Background(), bson.M{
		"_id": "w3", "name": "w3", "organizationId": single, "enabled": true, "payload": bson.M{},
		"rule": bson.M{"filter": `data.headers["x-priority"] == ["1"]`},
	}); err != nil {
		t.Fatal(err)
	}
	n2, err := (&NeedsResolver{DB: h.db}).Needs(context.Background(), single)
	if err != nil || !slices.Equal(n2.Headers, []string{"x-priority"}) || n2.Text {
		t.Fatalf("single header needs = %+v %v", n2, err)
	}
}

func (h *webhookHarness) w73Receiver(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	t.Cleanup(srv.Close)
	return srv.URL
}

// TestIntegrationW73EndpointRuleAPIWrappers drives the huma handlers and the error branches of the endpoint
// rule service methods.
func TestIntegrationW73EndpointRuleAPIWrappers(t *testing.T) {
	h := newWebhookHarness(t)
	ctx := h.ctx
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, m := range []store.Migration{rules.W73Migration(45), jobs.Migration(2)} {
		if err := m.Up(ctx, h.db); err != nil {
			t.Fatal(err)
		}
	}
	h.rules.Jobs = &jobs.Service{DB: h.db, NodeID: "n1", Log: quiet}
	h.rules.Stats = rules.NewStatsRecorder(h.db)
	m := &Module{service: h.service}

	plain, err := h.service.CreateEndpoint(ctx, EndpointCreate{
		Name: "plain", URL: h.w73Receiver(t), Events: []string{"*"}, Secret: testSecret(71),
	})
	if err != nil {
		t.Fatal(err)
	}
	if plain.Rule != nil {
		t.Fatalf("no rule expected: %+v", plain.Rule)
	}
	if _, err := m.testEndpointRule(ctx, &endpointRuleTestInput{EndpointID: plain.ID}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("test without rule: %v", err)
	}
	if _, err := m.dryRunEndpointRule(ctx, &endpointRuleDryRunInput{EndpointID: plain.ID}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("dry-run without rule: %v", err)
	}

	builder := map[string]any{"match": "all", "conditions": []any{
		map[string]any{"field": "isInbox", "op": "eq", "value": true},
		map[string]any{"match": "any", "conditions": []any{
			map[string]any{"field": "from.domain", "op": "eq", "value": "partner.com"},
			map[string]any{"field": "hasAttachments", "op": "eq", "value": true},
		}},
	}}
	ep, err := h.service.CreateEndpoint(ctx, EndpointCreate{
		Name: "built", URL: h.w73Receiver(t), Events: []string{"*"}, Secret: testSecret(72),
		Rule: &EndpointRule{Builder: builder, Map: `{"s": subject()}`},
	})
	if err != nil {
		t.Fatal(err)
	}
	if ep.Rule == nil || ep.Rule.Filter == "" || ep.Rule.CurrentVersion != 1 {
		t.Fatalf("builder rule not compiled: %+v", ep.Rule)
	}
	// Same builder → same version; a changed builder → new version.
	same, err := h.service.UpdateEndpoint(ctx, ep.ID, EndpointUpdate{Rule: &EndpointRule{Builder: builder, Map: `{"s": subject()}`}}, formatETag(ep.Version))
	if err != nil || same.Rule.CurrentVersion != 1 {
		t.Fatalf("same builder: %+v %v", same.Rule, err)
	}
	builder["match"] = "any"
	changed, err := h.service.UpdateEndpoint(ctx, ep.ID, EndpointUpdate{Rule: &EndpointRule{Builder: builder, Map: `{"s": subject()}`}}, formatETag(same.Version))
	if err != nil || changed.Rule.CurrentVersion != 2 {
		t.Fatalf("changed builder: %+v %v", changed.Rule, err)
	}

	res, err := m.testEndpointRule(ctx, &endpointRuleTestInput{EndpointID: ep.ID, Body: rules.RouteTestRequest{
		Event: map[string]any{"type": "message.received", "data": map[string]any{
			"mailbox": map[string]any{"path": "INBOX"}, "subject": "hello",
			"from": map[string]any{"address": "x@partner.com"}}},
	}})
	if err != nil || !res.Body.Matched || res.Body.Mapped.(map[string]any)["s"] != "hello" {
		t.Fatalf("test wrapper = %+v %v", res, err)
	}
	if _, err := m.getEndpointRuleStats(ctx, &endpointIDInput{EndpointID: ep.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.listEndpointRuleDecisions(ctx, &endpointRuleDecisionsInput{EndpointID: ep.ID, Limit: 5}); err != nil {
		t.Fatal(err)
	}
	vers, err := m.listEndpointRuleVersions(ctx, &endpointRuleVersionsInput{EndpointID: ep.ID, Limit: 1, Page: 2, IncludeTotal: true})
	if err != nil || len(vers.Body.Data) != 1 || vers.Body.Data[0].Version != 1 || *vers.Body.Total != 2 {
		t.Fatalf("versions page 2 = %+v %v", vers, err)
	}
	restored, err := m.restoreEndpointRuleVersion(ctx, &endpointRuleRestoreInput{EndpointID: ep.ID, Version: 1, IfMatch: formatETag(changed.Version)})
	if err != nil || restored.Body.Rule.CurrentVersion != 3 || restored.ETag == "" {
		t.Fatalf("restore wrapper = %+v %v", restored, err)
	}
	dry, err := m.dryRunEndpointRule(ctx, &endpointRuleDryRunInput{EndpointID: ep.ID, Body: rules.DryRunRequest{WindowHours: 1}})
	if err != nil || dry.Status != 202 || dry.Location == "" {
		t.Fatalf("dry-run wrapper = %+v %v", dry, err)
	}
	if _, err := m.dryRunEndpointRule(ctx, &endpointRuleDryRunInput{EndpointID: ep.ID, Body: rules.DryRunRequest{WindowHours: 48}}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("dry-run window: %v", err)
	}

	// A stored rule that no longer compiles is skipped and counted, never delivered unfiltered.
	if _, err := h.db.Collection(EndpointsCollection).UpdateOne(ctx, bson.M{"_id": ep.ID},
		bson.M{"$set": bson.M{"rule.filter": "isInbox(", "rule.currentVersion": 9}}); err != nil {
		t.Fatal(err)
	}
	accountID := store.NewID(store.PrefixAccount)
	ev := h.w73Event(t, h.orgID, accountID, "INBOX", "broken", nil, time.Now().UTC())
	if err := h.service.RouteEvent(ctx, ev); err != nil {
		t.Fatal(err)
	}
	for _, d := range h.w73Deliveries(t, h.orgID, ev.ID) {
		if d.EndpointID == ep.ID {
			t.Fatalf("broken rule delivered: %+v", d)
		}
	}
	st, err := h.service.EndpointRuleStats(ctx, ep.ID)
	if err != nil || st.Errors != 1 || st.LastError == "" {
		t.Fatalf("broken rule stats = %+v %v", st, err)
	}
	// Without the rules repo the stats/decisions endpoints answer empty, and /test is unavailable.
	saved := h.service.Rules
	h.service.Rules = nil
	if st, err := h.service.EndpointRuleStats(ctx, ep.ID); err != nil || st.Evaluated != 0 {
		t.Fatalf("nil rules stats = %+v %v", st, err)
	}
	if d, err := h.service.EndpointRuleDecisions(ctx, ep.ID, 1); err != nil || len(d.Data) != 0 {
		t.Fatalf("nil rules decisions = %+v %v", d, err)
	}
	if _, err := h.service.TestEndpointRule(ctx, ep.ID, rules.RouteTestRequest{Sample: "message.received"}); errs.CodeOf(err) != errs.CodeInternalError {
		t.Fatalf("nil rules test: %v", err)
	}
	if _, err := h.service.DryRunEndpointRule(ctx, ep.ID, rules.DryRunRequest{}); errs.CodeOf(err) != errs.CodeInternalError {
		t.Fatalf("nil rules dry-run: %v", err)
	}
	h.service.Rules = saved
	if !sameBuilder(nil, nil) || sameBuilder(nil, map[string]any{}) || !sameBuilder(map[string]any{"a": 1}, map[string]any{"a": 1}) {
		t.Fatal("sameBuilder")
	}
}
