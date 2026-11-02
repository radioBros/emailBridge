package configcode

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/apikeys"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/jobs"
	"github.com/radiobros/emailbridge/internal/rules"
	"github.com/radiobros/emailbridge/internal/settings"
	"github.com/radiobros/emailbridge/internal/tenancy"
	"github.com/radiobros/emailbridge/internal/webhook"
)

func TestApplyDrift409(t *testing.T) {
	svc := &Service{SkipFeatureGate: true}
	ctx := withCaller(context.Background(), "org_1", tenancy.PermSettingsWrite)
	doc := Document{
		APIVersion: APIVersion, Kind: KindOrg,
		Spec: Spec{WebhookEndpoints: []EndpointSpec{{Name: "a", URL: "https://a", Events: []string{"*"}}}},
	}
	plan, err := svc.Plan(ctx, doc, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Actions) == 0 {
		t.Fatal("expected create action")
	}
	_, err = svc.Apply(ctx, doc, false, "sha256:deadbeef")
	if errs.CodeOf(err) != errs.CodeConfigDrift {
		t.Fatalf("want config_drift, got %v", err)
	}
}

func TestHandleApplyStopsOnError(t *testing.T) {
	svc := &Service{
		SkipFeatureGate: true,
		ResolveSecret:   func(string) (string, error) { return "x", nil },
	}
	ctx := withCaller(context.Background(), "org_1", tenancy.PermSettingsWrite, tenancy.PermWebhooksWrite)
	doc := Document{
		APIVersion: APIVersion, Kind: KindOrg,
		Spec: Spec{WebhookEndpoints: []EndpointSpec{
			{Name: "a", URL: "https://a.example", Events: []string{"*"}, Secret: "${secret:A}"},
		}},
	}
	plan, err := svc.Plan(ctx, doc, false)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(ApplyRequest{Document: doc, PlanHash: plan.PlanHash})
	j := &jobs.Job{Payload: payload}
	res, err := svc.handleApply(ctx, j, nopReporter{})
	if err == nil {
		t.Fatal("expected error")
	}
	ar, ok := res.(*ApplyResult)
	if !ok || ar.StoppedAt == nil {
		t.Fatalf("stoppedAt missing: %#v", res)
	}
	if ar.StoppedAt.Kind != KindEndpoint {
		t.Fatalf("stopped=%#v", ar.StoppedAt)
	}
}

func TestExportRequiresAuth(t *testing.T) {
	svc := &Service{SkipFeatureGate: true}
	_, err := svc.Export(context.Background())
	if err == nil {
		t.Fatal("expected auth error")
	}
}

func TestRequireEnabledFeatureGate(t *testing.T) {
	st := &fakeSettings{body: settings.SettingsBody{
		Switches: map[string]settings.SettingValue{"configAsCode.enabled": sv(false, "organization")},
	}}
	svc := &Service{Settings: st}
	ctx := withCaller(context.Background(), "org_1")
	if err := svc.requireEnabled(ctx); errs.CodeOf(err) != errs.CodeFeatureDisabled {
		t.Fatalf("got %v", err)
	}
	st.body.Switches["configAsCode.enabled"] = sv(true, "organization")
	if err := svc.requireEnabled(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestApplyQueuesJob(t *testing.T) {
	j := &fakeJobs{}
	aud := &fakeAudit{}
	svc := &Service{SkipFeatureGate: true, Jobs: j, Audit: aud}
	ctx := withCaller(context.Background(), "org_1")
	doc := Document{APIVersion: APIVersion, Kind: KindOrg, Spec: Spec{}}
	plan, err := svc.Plan(ctx, doc, false)
	if err != nil {
		t.Fatal(err)
	}
	job, err := svc.Apply(ctx, doc, false, plan.PlanHash)
	if err != nil {
		t.Fatal(err)
	}
	if job.ID != "job_1" || len(j.created) != 1 {
		t.Fatalf("job=%#v created=%d", job, len(j.created))
	}
	if len(aud.actions) == 0 || aud.actions[0] != "config.apply.queued" {
		t.Fatalf("audit=%v", aud.actions)
	}
	_, err = svc.Apply(ctx, doc, false, "")
	if errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("empty hash: %v", err)
	}
	svc.Jobs = nil
	_, err = svc.Apply(ctx, doc, false, plan.PlanHash)
	if errs.CodeOf(err) != errs.CodeInternalError {
		t.Fatalf("nil jobs: %v", err)
	}
}

func TestRegisterHandlers(t *testing.T) {
	j := &fakeJobs{}
	svc := &Service{Jobs: j}
	svc.RegisterHandlers()
	if j.handlers[JobKind] == nil {
		t.Fatal("handler not registered")
	}
	(&Service{}).RegisterHandlers() // nil jobs no-op
}

func TestExportAndLoadLive(t *testing.T) {
	en := true
	st := &fakeSettings{body: settings.SettingsBody{
		Version: 4,
		Values: map[string]settings.SettingValue{
			"webhookTimeout": sv("20s", "organization"),
			"globalOnly":     sv("x", "global"),
		},
		Switches: map[string]settings.SettingValue{
			"tracking.opens": sv(false, "organization"),
		},
	}}
	wh := &fakeWebhooks{endpoints: []webhook.EndpointView{{
		ID: "ep_1", Name: "billing", Kind: "http", Enabled: true, URL: "https://b.example",
		Events: []string{"message.received"}, SecretPreview: "whsec_…", Version: 3,
		Security: webhook.SecurityView{SignatureMode: "standard"},
		Filter:   webhook.EndpointFilter{AccountIDs: []string{"acc_1"}},
		Payload:  webhook.PayloadOptions{Text: "plain"},
		Retry:    webhook.RetryOptions{MaxAttempts: 3, BackoffBase: "1s"},
		Delivery: webhook.DeliveryOptions{Ordering: "strict", MaxConcurrency: 1, ContentType: "json"},
		Switches: map[string]any{"x": true},
	}}}
	rt := &fakeRoutes{routes: []rules.Route{{
		ID: "rt_1", Name: "to-billing", EndpointID: "ep_1", Enabled: true,
		Filter: "true", Map: "{}", Order: 1, Version: 2,
	}}}
	ak := &fakeAPIKeys{keys: []apikeys.Key{
		{ID: "key_1", Name: "ci", Scopes: []string{"events:read"}, Version: 1},
		{ID: "key_2", Name: "gone", Scopes: []string{"read"}, Version: 1, RevokedAt: ptrTime(time.Now())},
	}}
	svc := &Service{
		SkipFeatureGate: true,
		Settings:        st,
		Webhooks:        wh,
		Routes:          rt,
		APIKeys:         ak,
		Orgs:            &fakeOrgs{slug: "acme"},
	}
	ctx := withCaller(context.Background(), "org_1")
	doc, err := svc.Export(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Metadata.Organization != "acme" {
		t.Fatalf("slug=%q", doc.Metadata.Organization)
	}
	if doc.Spec.Settings["webhookTimeout"] != "20s" || doc.Spec.Settings["globalOnly"] != nil {
		t.Fatalf("settings=%v", doc.Spec.Settings)
	}
	if len(doc.Spec.WebhookEndpoints) != 1 || doc.Spec.WebhookEndpoints[0].Secret != "${secret:WEBHOOK_BILLING_SECRET}" {
		t.Fatalf("endpoints=%+v", doc.Spec.WebhookEndpoints)
	}
	if len(doc.Spec.APIKeys) != 1 || doc.Spec.APIKeys[0].Name != "ci" {
		t.Fatalf("keys=%+v", doc.Spec.APIKeys)
	}
	_ = en
	live, err := svc.loadLive(ctx)
	if err != nil || live.SettingsV != 4 || live.Endpoints["billing"].HasSecret != true {
		t.Fatalf("live=%#v err=%v", live, err)
	}
}

func TestHandleApplyFullSuccess(t *testing.T) {
	st := &fakeSettings{body: settings.SettingsBody{Version: 1}}
	wh := &fakeWebhooks{}
	rt := &fakeRoutes{}
	ak := &fakeAPIKeys{}
	aud := &fakeAudit{err: errs.New(errs.CodeInternalError, "audit down")} // warn path
	svc := &Service{
		SkipFeatureGate: true,
		Settings:        st,
		Webhooks:        wh,
		Routes:          rt,
		APIKeys:         ak,
		Audit:           aud,
		ResolveSecret:   func(string) (string, error) { return "resolved", nil },
	}
	ctx := withCaller(context.Background(), "org_1")
	en := true
	ord := 0
	doc := Document{
		APIVersion: APIVersion, Kind: KindOrg,
		Spec: Spec{
			Settings: map[string]any{"webhookTimeout": "20s"},
			Switches: map[string]any{"tracking.opens": false},
			WebhookEndpoints: []EndpointSpec{{
				Name: "billing", URL: "https://b.example/h", Events: []string{"*"},
				Secret: "${secret:BILLING}", Enabled: &en,
				Headers: []webhook.WebhookHeader{{Name: "X-Tok", Value: "${secret:HDR}", Secret: true}},
			}},
			Routes: []RouteSpec{{
				Name: "r1", Endpoint: "billing", Filter: "true", Enabled: &en, Order: &ord,
				Secret: "${secret:ROUTE}",
			}},
			APIKeys: []APIKeySpec{{Name: "ci", Scopes: []string{"events:read"}}},
		},
	}
	plan, err := svc.Plan(ctx, doc, false)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(ApplyRequest{Document: doc, PlanHash: plan.PlanHash})
	res, err := svc.handleApply(ctx, &jobs.Job{Payload: payload}, nopReporter{})
	if err != nil {
		t.Fatal(err)
	}
	ar := res.(*ApplyResult)
	if len(ar.Applied) == 0 || ar.StoppedAt != nil {
		t.Fatalf("result=%#v createdEP=%d createdRT=%d createdKey=%d patched=%d",
			ar, len(wh.created), len(rt.created), len(ak.created), len(st.patched))
	}
	if len(wh.created) != 1 || wh.created[0].Secret != "resolved" {
		t.Fatalf("endpoint create=%+v", wh.created)
	}
	if len(rt.created) != 1 || rt.created[0].EndpointID != "ep_new" {
		t.Fatalf("route create=%+v", rt.created)
	}
	if len(ak.created) != 1 {
		t.Fatalf("keys=%+v", ak.created)
	}
}

func TestHandleApplyUpdatesAndDeletes(t *testing.T) {
	en := true
	st := &fakeSettings{body: settings.SettingsBody{
		Version:  2,
		Values:   map[string]settings.SettingValue{"webhookTimeout": sv("10s", "organization")},
		Switches: map[string]settings.SettingValue{"old.sw": sv(true, "organization")},
	}}
	wh := &fakeWebhooks{endpoints: []webhook.EndpointView{
		{ID: "ep_1", Name: "billing", Enabled: true, URL: "https://old", Events: []string{"*"}, Version: 3, SecretPreview: "x"},
		{ID: "ep_x", Name: "orphan", Enabled: true, URL: "https://x", Events: []string{"*"}, Version: 1},
	}}
	rt := &fakeRoutes{routes: []rules.Route{
		{ID: "rt_1", Name: "r1", EndpointID: "ep_1", Enabled: true, Filter: "old", Version: 1},
		{ID: "rt_x", Name: "gone", EndpointID: "ep_x", Enabled: true, Filter: "true", Version: 1},
	}}
	ak := &fakeAPIKeys{keys: []apikeys.Key{
		{ID: "key_1", Name: "ci", Scopes: []string{"read"}, Version: 1},
		{ID: "key_x", Name: "legacy", Scopes: []string{"read"}, Version: 1},
	}}
	svc := &Service{
		SkipFeatureGate: true,
		Settings:        st, Webhooks: wh, Routes: rt, APIKeys: ak,
		ResolveSecret: func(string) (string, error) { return "s", nil },
		Audit:         &fakeAudit{},
	}
	ctx := withCaller(context.Background(), "org_1")
	doc := Document{
		APIVersion: APIVersion, Kind: KindOrg,
		Spec: Spec{
			Settings: map[string]any{"webhookTimeout": "20s"},
			Switches: map[string]any{},
			WebhookEndpoints: []EndpointSpec{{
				Name: "billing", URL: "https://new", Events: []string{"message.received"}, Enabled: &en,
				Secret: "${secret:S}",
			}},
			Routes:  []RouteSpec{{Name: "r1", Endpoint: "billing", Filter: "true", Enabled: &en}},
			APIKeys: []APIKeySpec{{Name: "ci", Scopes: []string{"events:read"}}},
		},
	}
	plan, err := svc.Plan(ctx, doc, true)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(ApplyRequest{Document: doc, Prune: true, PlanHash: plan.PlanHash})
	res, err := svc.handleApply(ctx, &jobs.Job{Payload: payload}, nopReporter{})
	if err != nil {
		t.Fatal(err)
	}
	ar := res.(*ApplyResult)
	if ar.StoppedAt != nil {
		t.Fatalf("stopped=%#v", ar.StoppedAt)
	}
	if len(wh.updated) == 0 || len(wh.deleted) == 0 {
		t.Fatalf("wh updated=%d deleted=%v", len(wh.updated), wh.deleted)
	}
	if len(rt.updated) == 0 || len(rt.deleted) == 0 {
		t.Fatalf("rt updated=%d deleted=%v", len(rt.updated), rt.deleted)
	}
	if len(ak.patched) == 0 || len(ak.revoked) == 0 {
		t.Fatalf("ak patched=%d revoked=%v", len(ak.patched), ak.revoked)
	}
	foundSwitchDelete := false
	for _, p := range st.patched {
		if sw, ok := p["switches"].(map[string]any); ok {
			if _, ok := sw["old.sw"]; ok {
				foundSwitchDelete = true
			}
		}
	}
	if !foundSwitchDelete {
		t.Fatalf("switch prune missing: %+v", st.patched)
	}
}

func TestHandleApplyCancelledAndBadPayload(t *testing.T) {
	svc := &Service{SkipFeatureGate: true}
	ctx := withCaller(context.Background(), "org_1")
	_, err := svc.handleApply(ctx, &jobs.Job{Payload: []byte(`{`)}, nopReporter{})
	if errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("bad payload: %v", err)
	}
	doc := Document{APIVersion: APIVersion, Kind: KindOrg, Spec: Spec{
		WebhookEndpoints: []EndpointSpec{{Name: "a", URL: "https://a", Events: []string{"*"}}},
	}}
	plan, _ := svc.Plan(ctx, doc, false)
	payload, _ := json.Marshal(ApplyRequest{Document: doc, PlanHash: plan.PlanHash})
	_, err = svc.handleApply(ctx, &jobs.Job{Payload: payload}, cancelReporter{})
	if errs.CodeOf(err) != errs.CodeConflict {
		t.Fatalf("cancel: %v", err)
	}
}

func TestApplyActionUnknownKind(t *testing.T) {
	svc := &Service{SkipFeatureGate: true}
	_, err := svc.applyAction(context.Background(), &Document{}, Action{Kind: "nope", Op: OpCreate})
	if errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatal(err)
	}
}

func TestEndpointRouteBuilders(t *testing.T) {
	svc := &Service{ResolveSecret: func(ref string) (string, error) {
		n, _ := SecretName(ref)
		return "val-" + n, nil
	}}
	en := true
	ep, err := svc.endpointCreate(context.Background(), EndpointSpec{
		Name: "e", Kind: "http", Enabled: &en, URL: "https://e", Events: []string{"*"},
		Secret:     "${secret:E}",
		Filter:     &webhook.EndpointFilter{AccountIDs: []string{"a"}},
		Payload:    &webhook.PayloadOptions{Text: "t"},
		Retry:      &webhook.RetryOptions{MaxAttempts: 2},
		Delivery:   &webhook.DeliveryUpdate{Ordering: strPtr("strict")},
		Security:   &webhook.SecurityWrite{SignatureMode: "standard"},
		Headers:    []webhook.WebhookHeader{{Name: "H", Value: "${secret:H}", Secret: true}},
		Switches:   map[string]any{"x": 1},
		SinkConfig: map[string]any{"t": "v"},
	})
	if err != nil || ep.Secret != "val-E" || ep.Headers[0].Value != "val-H" {
		t.Fatalf("%+v %v", ep, err)
	}
	up, err := svc.endpointUpdate(context.Background(), EndpointSpec{
		Name: "e", URL: "https://e2", Events: []string{"x"}, Secret: "${secret:E2}",
		Security:   &webhook.SecurityWrite{SignatureMode: "bodyOnly"},
		SinkConfig: map[string]any{"a": 1},
		Headers:    []webhook.WebhookHeader{{Name: "H", Value: "${secret:H2}", Secret: true}},
	})
	if err != nil || up.Secret == nil || *up.Secret != "val-E2" {
		t.Fatalf("%+v %v", up, err)
	}
	wh := &fakeWebhooks{endpoints: []webhook.EndpointView{{ID: "ep_1", Name: "billing"}}}
	svc.Webhooks = wh
	ctx := context.Background()
	rc, err := svc.routeCreate(ctx, RouteSpec{Name: "r", Endpoint: "billing", Filter: "true", Secret: "${secret:R}"})
	if err != nil || rc.EndpointID != "ep_1" || rc.Secret != "val-R" {
		t.Fatalf("%+v %v", rc, err)
	}
	ru, err := svc.routeUpdate(ctx, RouteSpec{Name: "r", Endpoint: "billing", Filter: "false", Secret: "${secret:R2}"})
	if err != nil || ru.Secret == nil || *ru.Secret != "val-R2" {
		t.Fatalf("%+v %v", ru, err)
	}
	_, err = svc.routeCreate(ctx, RouteSpec{Name: "r", Endpoint: "missing", Filter: "true"})
	if errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatal(err)
	}
}

func TestHelpers(t *testing.T) {
	if etag(0) != "" || etag(3) != `"v3"` {
		t.Fatal(etag(3))
	}
	if findEndpoint(&Document{Spec: Spec{WebhookEndpoints: []EndpointSpec{{Name: "a"}}}}, "a").Name != "a" {
		t.Fatal("find ep")
	}
	if findRoute(&Document{}, "x").Name != "x" || findAPIKey(&Document{}, "k").Name != "k" {
		t.Fatal("find defaults")
	}
	if FormatSecretRef("X") != "${secret:X}" {
		t.Fatal("format")
	}
	if secretEnvName("WEBHOOK", "my-ep") != "WEBHOOK_MY_EP_SECRET" {
		t.Fatal(secretEnvName("WEBHOOK", "my-ep"))
	}
	if cloneMap(nil) != nil || cloneMap(map[string]any{"a": 1})["a"] != 1 {
		t.Fatal("clone")
	}
	f := ptrFilter(webhook.EndpointFilter{})
	if f != nil {
		t.Fatal("empty filter")
	}
	if ptrPayload(webhook.PayloadOptions{}) != nil || ptrRetry(webhook.RetryOptions{}) != nil {
		t.Fatal("empty payload/retry")
	}
	d := deliveryFrom(webhook.DeliveryOptions{Ordering: "strict", MaxConcurrency: 2, ContentType: "json"})
	if d.Ordering == nil || *d.Ordering != "strict" {
		t.Fatal(d)
	}
	svc := &Service{}
	if svc.log() == nil {
		t.Fatal("default log")
	}
}

type nopReporter struct{}

func (nopReporter) Progress(context.Context, int64, int64) {}
func (nopReporter) Cancelled() bool                        { return false }

type cancelReporter struct{}

func (cancelReporter) Progress(context.Context, int64, int64) {}
func (cancelReporter) Cancelled() bool                        { return true }

func withCaller(ctx context.Context, orgID string, _ ...tenancy.Permission) context.Context {
	return tenancy.WithCaller(ctx, tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: "usr_1", OrganizationID: orgID, Role: tenancy.RoleOwner,
	})
}

func ptrTime(t time.Time) *time.Time { return &t }
