package configcode

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/jobs"
	"github.com/radiobros/emailbridge/internal/tenancy"
	"github.com/radiobros/emailbridge/internal/webhook"
)

func TestModuleLifecycleAndAPI(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv, err := httpapi.New(httpapi.Options{AccessLog: quiet, SecurityLog: quiet})
	if err != nil {
		t.Fatal(err)
	}
	j := &fakeJobs{}
	a := app.New(app.Options{Log: quiet, Roles: app.RoleSet{app.RoleAPI: true}})
	app.Provide(a, srv)

	m := &Module{Log: quiet}
	if m.Name() != "configcode" || len(m.Roles()) != 1 || m.Roles()[0] != app.RoleAPI {
		t.Fatalf("meta name=%s roles=%v", m.Name(), m.Roles())
	}
	if err := m.Start(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if m.Service() == nil {
		t.Fatal("service nil")
	}
	m.Service().SkipFeatureGate = true
	m.Service().Jobs = j
	m.Service().RegisterHandlers()
	if err := m.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}

	ctx := withCaller(context.Background(), "org_1")
	out, err := m.exportConfig(ctx, nil)
	if err != nil || out.Body == nil {
		t.Fatalf("export: %v %#v", err, out)
	}
	planOut, err := m.planConfig(ctx, &planIn{Body: PlanRequest{Document: Document{
		APIVersion: APIVersion, Kind: KindOrg,
	}}})
	if err != nil || planOut.Body == nil {
		t.Fatalf("plan: %v", err)
	}
	applyOut, err := m.applyConfig(ctx, &applyIn{Body: ApplyRequest{
		Document: Document{APIVersion: APIVersion, Kind: KindOrg},
		PlanHash: planOut.Body.PlanHash,
	}})
	if err != nil || applyOut.Status != http.StatusAccepted {
		t.Fatalf("apply: %v %#v", err, applyOut)
	}
	if !strings.HasPrefix(applyOut.Location, "/v1/jobs/") {
		t.Fatalf("location=%s", applyOut.Location)
	}

	_, err = m.exportConfig(context.Background(), nil)
	if err == nil {
		t.Fatal("export unauth")
	}
	_, err = m.planConfig(ctx, &planIn{Body: PlanRequest{Document: Document{
		Spec: Spec{WebhookEndpoints: []EndpointSpec{{Name: "x", Secret: "inline"}}},
	}}})
	if err == nil {
		t.Fatal("plan validation")
	}
	_, err = m.applyConfig(ctx, &applyIn{Body: ApplyRequest{
		Document: Document{APIVersion: APIVersion}, PlanHash: "sha256:nope",
	}})
	if errs.CodeOf(err) != errs.CodeConfigDrift {
		t.Fatalf("drift via handler: %v", err)
	}

	mw := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(tenancy.WithCaller(r.Context(), tenancy.Caller{
				Kind: tenancy.CallerUser, UserID: "usr_1", OrganizationID: "org_1", Role: tenancy.RoleOwner,
			})))
		})
	}
	req := httptest.NewRequest(http.MethodGet, "/v1/config/export", nil)
	w := httptest.NewRecorder()
	mw(srv).ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("export HTTP %d %s", w.Code, w.Body.String())
	}
}

func TestMoreServiceEdges(t *testing.T) {
	ctx := withCaller(context.Background(), "org_1")
	st := &fakeSettings{}
	svc := &Service{Settings: st}
	if err := svc.requireEnabled(ctx); errs.CodeOf(err) != errs.CodeFeatureDisabled {
		t.Fatalf("missing switch: %v", err)
	}

	svc = &Service{SkipFeatureGate: true, Env: &fakeEnv{}}
	dir := t.TempDir()
	path := dir + string(os.PathSeparator) + "sec"
	if err := os.WriteFile(path, []byte("fromfile\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Server files and the process environment are never reachable from a document (security fix).
	t.Setenv("FILESEC_FILE", path)
	t.Setenv("FILESEC", "from-env")
	v, err := svc.resolve(ctx, "${secret:FILESEC}")
	if errs.CodeOf(err) != errs.CodeValidationFailed || v != "" {
		t.Fatalf("file/env secret must not resolve: %q %v", v, err)
	}
	if redactSecretChange(false) != "(unchanged)" {
		t.Fatal("unchanged")
	}

	wh := &fakeWebhooks{pages: 1, endpoints: []webhook.EndpointView{{ID: "ep_1", Name: "a", Enabled: true}}}
	svc.Webhooks = wh
	eps, err := svc.listAllEndpoints(ctx)
	if err != nil || len(eps) != 1 {
		t.Fatalf("paged list: %v %d", err, len(eps))
	}

	if err := svc.applySettings(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if err := (&Service{SkipFeatureGate: true}).applySwitch(ctx, "x", OpCreate, map[string]any{"x": true}); err != nil {
		t.Fatal(err)
	}
	svc.Webhooks = nil
	if err := svc.applyEndpoint(ctx, Action{Op: OpCreate, Name: "a"}, &Document{}); errs.CodeOf(err) != errs.CodeInternalError {
		t.Fatal(err)
	}
	if err := svc.applyRoute(ctx, Action{Op: OpCreate, Name: "a"}, &Document{}); errs.CodeOf(err) != errs.CodeInternalError {
		t.Fatal(err)
	}
	if _, err := svc.applyAPIKey(ctx, Action{Op: OpCreate, Name: "a"}, &Document{}); errs.CodeOf(err) != errs.CodeInternalError {
		t.Fatal(err)
	}

	p, err := (&Service{SkipFeatureGate: true}).Plan(ctx, Document{}, false)
	if err != nil || p == nil {
		t.Fatal(err)
	}

	doc := Document{APIVersion: APIVersion, Spec: Spec{
		WebhookEndpoints: []EndpointSpec{{Name: "n", URL: "https://n", Events: []string{"*"}}},
	}}
	plan, err := (&Service{SkipFeatureGate: true}).Plan(ctx, doc, false)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(ApplyRequest{Document: doc, PlanHash: "sha256:deadbeef"})
	_, err = (&Service{SkipFeatureGate: true}).handleApply(ctx, &jobs.Job{Payload: payload}, nopReporter{})
	if errs.CodeOf(err) != errs.CodeConfigDrift {
		t.Fatalf("handle drift: %v (plan was %s)", err, plan.PlanHash)
	}

	svc2 := &Service{SkipFeatureGate: true, Log: slog.Default()}
	if svc2.log() == nil {
		t.Fatal("log")
	}
	findEndpoint(&Document{Spec: Spec{WebhookEndpoints: []EndpointSpec{{Name: "z"}}}}, "missing")
}
