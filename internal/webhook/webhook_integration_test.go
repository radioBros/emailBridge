//go:build integration

package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/bus"
	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/events"
	eventtypes "github.com/radiobros/emailbridge/internal/events/types"
	"github.com/radiobros/emailbridge/internal/httpapi"
	"github.com/radiobros/emailbridge/internal/rules"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
	"github.com/radiobros/emailbridge/pkg/webhookverify"
)

type publishedMessage struct {
	subject, id string
	data        []byte
}

type capturePublisher struct {
	mu       sync.Mutex
	messages []publishedMessage
}

type staticResolver map[string][]netip.Addr

func (r staticResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	return r[host], nil
}

func (p *capturePublisher) Publish(_ context.Context, subject string, data []byte, id string, _ map[string]string) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.messages = append(p.messages, publishedMessage{subject: subject, id: id, data: append([]byte(nil), data...)})
	return true, nil
}

type webhookHarness struct {
	db        *store.DB
	service   *Service
	rules     *rules.Repo
	publisher *capturePublisher
	ctx       context.Context
	orgID     string
	now       time.Time
}

func newWebhookHarness(t *testing.T) *webhookHarness {
	t.Helper()
	uri := os.Getenv("EB_TEST_MONGO_URI")
	if uri == "" {
		uri = "mongodb://127.0.0.1:27017/?replicaSet=rs0&directConnection=true"
	}
	root := context.Background()
	db, err := store.Connect(root, store.Options{
		URI: uri, Database: fmt.Sprintf("ebtest_webhook_%d", time.Now().UnixNano()), ConnectTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Skipf("no MongoDB: %v", err)
	}
	t.Cleanup(func() { _ = db.DB.Drop(root); _ = db.Close(root) })
	for _, migration := range []store.Migration{
		Migration(1), events.Migration(1), rules.Migration(1), audit.Migration(1),
	} {
		if err := migration.Up(root, db); err != nil {
			t.Fatal(err)
		}
	}
	keyStore := &crypto.MongoKeyStore{DB: db}
	if err := db.EnsureIndexes(root, crypto.DataKeysCollection, keyStore.Indexes()...); err != nil {
		t.Fatal(err)
	}
	local, err := crypto.NewLocal([]crypto.LocalKey{{ID: "test", Key: bytes.Repeat([]byte{7}, crypto.KeySize)}}, "")
	if err != nil {
		t.Fatal(err)
	}
	cipher := &crypto.Cipher{
		KEK: local, Store: keyStore, NewID: func() string { return store.NewID(store.PrefixDataKey) },
	}
	engine, err := rules.NewEngine(rules.EngineOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ruleRepo := &rules.Repo{DB: db, Engine: engine, Cipher: cipher}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	publisher := &capturePublisher{}
	orgID := store.NewID(store.PrefixOrganization)
	ctx := tenancy.WithCaller(root, tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: store.NewID(store.PrefixUser),
		OrganizationID: orgID, Role: tenancy.RoleOwner,
	})
	h := &webhookHarness{
		db: db, rules: ruleRepo, publisher: publisher, ctx: ctx, orgID: orgID,
		now: time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC),
	}
	repo := &Repo{DB: db}
	h.service = &Service{
		Repo: repo, Cipher: cipher, Egress: egress.NewDialer(egress.Policy{}),
		Events: &events.Writer{DB: db}, Rules: ruleRepo, Engine: engine, Publisher: publisher,
		Audit: &audit.Writer{DB: db, Log: quiet, Retention: 24 * time.Hour},
		Log:   quiet, SecurityLog: quiet, DeliveryPartitions: 4, Concurrency: 4,
		Retention: 24 * time.Hour, RotationOverlap: time.Hour, RandFloat: func() float64 { return .5 },
		Now: func() time.Time { return h.now },
	}
	return h
}

func (h *webhookHarness) caller(orgID string, role tenancy.Role) context.Context {
	return tenancy.WithCaller(context.Background(), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: store.NewID(store.PrefixUser),
		OrganizationID: orgID, Role: role,
	})
}

func (h *webhookHarness) endpoint(t *testing.T, name, url, accountID string) *Endpoint {
	t.Helper()
	secret := testSecret(byte(len(name) + 1))
	endpoint, err := h.service.CreateEndpoint(h.ctx, EndpointCreate{
		Name: name, URL: url, Events: []string{events.MessageReceived},
		Filter:  EndpointFilter{AccountIDs: []string{accountID}},
		Headers: []WebhookHeader{{Name: "X-API-Key", Value: "header-" + name, Secret: true}},
		Payload: PayloadOptions{Text: "plain", TextMax: 64, Headers: []string{"subject"}, Attachments: true},
		Secret:  secret,
		Retry:   RetryOptions{MaxAttempts: 3, BackoffBase: "1s", BackoffJitter: .2, Timeout: "2s"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return endpoint
}

func (h *webhookHarness) event(t *testing.T, accountID, subject string) events.Event {
	t.Helper()
	messageID := store.NewID(store.PrefixMessage)
	got, err := h.service.Events.Emit(h.ctx, events.Draft{
		Type: events.MessageReceived, OrganizationID: h.orgID, AccountID: accountID,
		Subject: messageID, Time: h.now,
		Data: &eventtypes.MessageReceived{
			AccountRef: eventtypes.AccountRef{AccountID: accountID},
			MessageID:  messageID, Mailbox: eventtypes.Mailbox{MailboxID: store.NewID(store.PrefixMailbox), Path: "INBOX"},
			Subject: subject, Headers: map[string][]string{"subject": {subject}},
			Text:         &eventtypes.TextContent{Plain: "plain " + subject, HTML: "<b>html</b>", Size: 20},
			InternalDate: h.now,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return got[0]
}

func TestIntegrationWebhookLifecycle(t *testing.T) {
	h := newWebhookHarness(t)
	var receivedMu sync.Mutex
	var received []*http.Request
	var bodies [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		receivedMu.Lock()
		received = append(received, r.Clone(context.Background()))
		bodies = append(bodies, body)
		receivedMu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)

	accountID := store.NewID(store.PrefixAccount)
	endpoint := h.endpoint(t, "primary", server.URL, accountID)
	secret := testSecret(byte(len("primary") + 1))
	if endpoint.Kind != KindHTTP || endpoint.Delivery.Ordering != OrderingStrict || endpoint.Breaker.State != "closed" {
		t.Fatalf("defaults not applied: %+v", endpoint)
	}
	if endpointView(endpoint).SecretPreview == "" || endpointView(endpoint).Headers[0].Value != "" {
		t.Fatal("secret-safe endpoint view")
	}
	raw, _ := bson.Marshal(endpoint)
	if strings.Contains(string(raw), secret) || strings.Contains(string(raw), "header-primary") {
		t.Fatal("endpoint persisted plaintext secret")
	}
	if _, err := h.service.CreateEndpoint(h.ctx, EndpointCreate{
		Name: endpoint.Name, URL: server.URL, Events: []string{"*"}, Secret: testSecret(9),
	}); errs.CodeOf(err) != errs.CodeEndpointNameTaken {
		t.Fatalf("duplicate name: %v", err)
	}

	otherOrg := store.NewID(store.PrefixOrganization)
	if _, err := h.service.GetEndpoint(h.caller(otherOrg, tenancy.RoleOwner), endpoint.ID); errs.CodeOf(err) != errs.CodeNotFound {
		t.Fatalf("tenant isolation: %v", err)
	}
	if _, err := h.service.CreateEndpoint(h.caller(h.orgID, tenancy.RoleViewer), EndpointCreate{}); err == nil {
		t.Fatal("viewer created endpoint")
	}
	if _, err := h.service.GetEndpoint(h.caller(h.orgID, tenancy.RoleViewer), endpoint.ID); err != nil {
		t.Fatalf("viewer read: %v", err)
	}
	for _, tc := range []struct {
		role     tenancy.Role
		canWrite bool
	}{
		{tenancy.RoleOwner, true},
		{tenancy.RoleAdmin, true},
		{tenancy.RoleOperator, true},
		{tenancy.RoleViewer, false},
	} {
		roleCtx := h.caller(h.orgID, tc.role)
		if _, err := h.service.ListEndpoints(roleCtx, EndpointListFilter{}); err != nil {
			t.Errorf("%s read: %v", tc.role, err)
		}
		_, err := h.service.CreateEndpoint(roleCtx, EndpointCreate{})
		if tc.canWrite && errs.CodeOf(err) != errs.CodeValidationFailed {
			t.Errorf("%s write reached validation: %v", tc.role, err)
		}
		if !tc.canWrite && errs.CodeOf(err) != errs.CodeForbidden {
			t.Errorf("%s write: %v", tc.role, err)
		}
	}
	if _, err := h.service.UpdateEndpoint(h.ctx, endpoint.ID, EndpointUpdate{}, `"v99"`); errs.CodeOf(err) != errs.CodeVersionMismatch {
		t.Fatalf("version mismatch: %v", err)
	}
	updatedHeaders := []WebhookHeader{{Name: "X-API-Key", Value: "header-primary-updated", Secret: true}}
	updatedEndpoint, err := h.service.UpdateEndpoint(
		h.ctx,
		endpoint.ID,
		EndpointUpdate{Headers: &updatedHeaders},
		formatETag(endpoint.Version),
	)
	if err != nil {
		t.Fatal(err)
	}
	endpoint = updatedEndpoint
	name, targetURL := endpoint.Name, endpoint.URL
	eventTypes := append([]string(nil), endpoint.Events...)
	filter := endpoint.Filter
	pendingHeaders := []WebhookHeader{{Name: "X-API-Key", Secret: true}}
	payload := endpoint.Payload
	security := SecurityWrite{SignatureMode: endpoint.Security.SignatureMode}
	retryOptions := endpoint.Retry
	switches := map[string]any{"custom": true}
	endpoint, err = h.service.UpdateEndpoint(h.ctx, endpoint.ID, EndpointUpdate{
		Name: &name, URL: &targetURL, Events: &eventTypes,
		Filter: &filter, Headers: &pendingHeaders, Payload: &payload, Security: &security,
		Retry: &retryOptions, Switches: &switches,
	}, formatETag(endpoint.Version))
	if err != nil {
		t.Fatal(err)
	}

	route, err := h.rules.Create(h.ctx, rules.RouteCreate{
		Name: "mapped", EndpointID: endpoint.ID, Filter: "true", Map: `{"mapped": "original"}`,
		Secret: secret,
		Headers: map[string]any{
			"X-Route": "yes",
			"X-Obj":   map[string]any{"value": "object-value"},
		},
		Payload: map[string]any{"text": "both", "attachments": false},
	})
	if err != nil {
		t.Fatal(err)
	}
	event := h.event(t, accountID, "one")
	if err := h.service.RouteEvent(h.ctx, event); err != nil {
		t.Fatal(err)
	}
	if err := h.service.RouteEvent(h.ctx, event); err != nil {
		t.Fatal(err)
	}
	list, err := h.service.ListDeliveries(h.ctx, DeliveryFilter{EventID: event.ID, Sort: "id", IncludeTotal: true, Page: 1})
	if err != nil || len(list.Data) != 2 || list.Total == nil || *list.Total != 2 {
		t.Fatalf("dedup/list: %+v %v", list, err)
	}
	newMap := `{"mapped": "changed"}`
	if _, err := h.rules.Update(h.ctx, route.ID, rules.RouteUpdate{Map: &newMap}, fmt.Sprintf(`"v%d"`, route.Version)); err != nil {
		t.Fatal(err)
	}
	for i := range list.Data {
		if err := h.service.HandleDelivery(h.ctx, list.Data[i].ID, false); err != nil {
			t.Fatalf("deliver %d: %v", i, err)
		}
	}
	receivedMu.Lock()
	if len(received) != 2 {
		t.Fatalf("requests = %d", len(received))
	}
	for i, request := range received {
		if request.Header.Get("X-API-Key") != "header-primary-updated" ||
			request.Header.Get("eb-delivery-id") == "" ||
			request.Header.Get("eb-event-type") != events.MessageReceived {
			t.Fatalf("request headers = %v", request.Header)
		}
		if err := webhookverify.Verify(secret, request.Header, bodies[i], h.now, time.Minute); err != nil {
			t.Fatalf("signature: %v", err)
		}
	}
	if string(bodies[1]) != `{"mapped":"original"}` {
		t.Fatalf("queued mapping changed: %s", bodies[1])
	}
	if received[1].Header.Get("X-Route") != "yes" || received[1].Header.Get("X-Obj") != "object-value" {
		t.Fatalf("route headers = %v", received[1].Header)
	}
	receivedMu.Unlock()

	rotated, err := h.service.RotateSecret(h.ctx, endpoint.ID)
	if err != nil || rotated.Secret == "" || rotated.ExpiresAt == nil {
		t.Fatalf("rotate: %+v %v", rotated, err)
	}
	current, _ := h.service.GetEndpoint(h.ctx, endpoint.ID)
	secrets, err := h.service.decryptSecrets(h.ctx, current)
	if err != nil || len(secrets) != 2 || secrets[0] != rotated.Secret {
		t.Fatalf("rotation overlap: %v %v", secrets, err)
	}
	h.now = h.now.Add(2 * time.Hour)
	if secrets, err = h.service.decryptSecrets(h.ctx, current); err != nil || len(secrets) != 1 {
		t.Fatalf("expired rotation secret: %v %v", secrets, err)
	}
	if exists, err := h.service.EndpointExists(h.ctx, h.orgID, endpoint.ID); err != nil || !exists {
		t.Fatalf("endpoint exists: %v %v", exists, err)
	}
	if err := h.service.DeleteEndpoint(h.ctx, endpoint.ID, formatETag(current.Version)); errs.CodeOf(err) != errs.CodeConflict {
		t.Fatalf("route delete guard: %v", err)
	}

	replay, err := h.service.Replay(h.ctx, list.Data[0].ID)
	if err != nil || replay.ReplayOf != list.Data[0].ID {
		t.Fatalf("replay: %+v %v", replay, err)
	}
	if err := h.service.HandleDelivery(h.ctx, replay.ID, false); err != nil {
		t.Fatal(err)
	}
	replayed, _ := h.service.GetDelivery(h.ctx, replay.ID)
	if replayed.State != StateSucceeded {
		t.Fatalf("replay state = %s", replayed.State)
	}

	attempt, err := h.service.TestEndpoint(h.ctx, endpoint.ID)
	if err != nil || attempt.StatusCode != http.StatusNoContent {
		t.Fatalf("endpoint test: %+v %v", attempt, err)
	}
	testRows, _ := h.service.ListDeliveries(h.ctx, DeliveryFilter{EndpointID: endpoint.ID, EventType: events.EndpointTest})
	if len(testRows.Data) != 1 {
		t.Fatalf("test delivery rows = %d", len(testRows.Data))
	}

	serverAPI, err := httpapi.New(httpapi.Options{})
	if err != nil {
		t.Fatal(err)
	}
	module := &Module{service: h.service}
	module.registerAPI(serverAPI)
	request := httptest.NewRequest(http.MethodGet, "/v1/webhook-endpoints/"+endpoint.ID, nil)
	request = request.WithContext(h.caller(h.orgID, tenancy.RoleViewer))
	response := httptest.NewRecorder()
	serverAPI.ServeHTTP(response, request)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), rotated.Secret) {
		t.Fatalf("API response %d: %s", response.Code, response.Body.String())
	}
	if _, err := module.listEndpoints(h.ctx, &endpointListInput{Limit: 10}); err != nil {
		t.Fatal(err)
	}
	createdOutput, err := module.createEndpoint(h.ctx, &endpointCreateInput{Body: EndpointCreate{
		Name: "api-wrapper", URL: server.URL, Events: []string{"*"}, Secret: testSecret(31),
	}})
	if err != nil || createdOutput.Status != http.StatusCreated {
		t.Fatalf("API create: %+v %v", createdOutput, err)
	}
	apiName := "api-wrapper-updated"
	patchedOutput, err := module.patchEndpoint(h.ctx, &endpointPatchInput{
		EndpointID: createdOutput.Body.ID, IfMatch: createdOutput.ETag,
		Body: EndpointUpdate{Name: &apiName},
	})
	if err != nil || patchedOutput.Body.Name != apiName {
		t.Fatalf("API patch: %+v %v", patchedOutput, err)
	}
	if _, err := module.rotateSecret(h.ctx, &endpointIDInput{EndpointID: createdOutput.Body.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err := module.testEndpoint(h.ctx, &endpointIDInput{EndpointID: createdOutput.Body.ID}); err != nil {
		t.Fatal(err)
	}
	deliveryOutput, err := module.listDeliveries(h.ctx, &deliveryListInput{EndpointID: createdOutput.Body.ID})
	if err != nil || len(deliveryOutput.Body.Data) != 1 {
		t.Fatalf("API deliveries: %+v %v", deliveryOutput, err)
	}
	if _, err := module.getDelivery(h.ctx, &deliveryIDInput{DeliveryID: deliveryOutput.Body.Data[0].ID}); err != nil {
		t.Fatal(err)
	}
	if accepted, err := module.replayDelivery(h.ctx, &deliveryIDInput{DeliveryID: list.Data[0].ID}); err != nil ||
		accepted.Status != http.StatusAccepted {
		t.Fatalf("API replay: %+v %v", accepted, err)
	}
	if _, err := module.deleteEndpoint(h.ctx, &endpointDeleteInput{EndpointID: createdOutput.Body.ID}); err != nil {
		t.Fatal(err)
	}
	otherCtx := h.caller(store.NewID(store.PrefixOrganization), tenancy.RoleOwner)
	handlerErrors := []error{}
	_, handlerErr := module.listEndpoints(h.ctx, &endpointListInput{Sort: "bad"})
	handlerErrors = append(handlerErrors, handlerErr)
	_, handlerErr = module.createEndpoint(h.caller(h.orgID, tenancy.RoleViewer), &endpointCreateInput{})
	handlerErrors = append(handlerErrors, handlerErr)
	_, handlerErr = module.getEndpoint(otherCtx, &endpointIDInput{EndpointID: endpoint.ID})
	handlerErrors = append(handlerErrors, handlerErr)
	_, handlerErr = module.patchEndpoint(h.ctx, &endpointPatchInput{EndpointID: endpoint.ID, IfMatch: `"v999"`})
	handlerErrors = append(handlerErrors, handlerErr)
	_, handlerErr = module.deleteEndpoint(h.ctx, &endpointDeleteInput{EndpointID: endpoint.ID})
	handlerErrors = append(handlerErrors, handlerErr)
	_, handlerErr = module.rotateSecret(otherCtx, &endpointIDInput{EndpointID: endpoint.ID})
	handlerErrors = append(handlerErrors, handlerErr)
	_, handlerErr = module.testEndpoint(otherCtx, &endpointIDInput{EndpointID: endpoint.ID})
	handlerErrors = append(handlerErrors, handlerErr)
	_, handlerErr = module.listDeliveries(h.ctx, &deliveryListInput{From: h.now, To: h.now.Add(-time.Second)})
	handlerErrors = append(handlerErrors, handlerErr)
	missingDeliveryID := store.NewID(store.PrefixDelivery)
	_, handlerErr = module.getDelivery(h.ctx, &deliveryIDInput{DeliveryID: missingDeliveryID})
	handlerErrors = append(handlerErrors, handlerErr)
	_, handlerErr = module.replayDelivery(h.ctx, &deliveryIDInput{DeliveryID: missingDeliveryID})
	handlerErrors = append(handlerErrors, handlerErr)
	for i, handlerErr := range handlerErrors {
		if handlerErr == nil {
			t.Errorf("API handler error case %d succeeded", i)
		}
	}

	auditBytes, _ := json.Marshal(mustAuditRows(t, h))
	if strings.Contains(string(auditBytes), secret) || strings.Contains(string(auditBytes), rotated.Secret) ||
		strings.Contains(string(auditBytes), "header-primary") || strings.Contains(string(auditBytes), "header-primary-updated") {
		t.Fatalf("audit leaked a secret: %s", auditBytes)
	}
}

func TestIntegrationRetriesBreakerOrderingAndSSRF(t *testing.T) {
	h := newWebhookHarness(t)

	var retryCalls atomic.Int32
	retryServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if retryCalls.Add(1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(retryServer.Close)
	accountID := store.NewID(store.PrefixAccount)
	endpoint := h.endpoint(t, "retry", retryServer.URL, accountID)
	event := h.event(t, accountID, "retry")
	if err := h.service.RouteEvent(h.ctx, event); err != nil {
		t.Fatal(err)
	}
	rows, _ := h.service.ListDeliveries(h.ctx, DeliveryFilter{EndpointID: endpoint.ID, EventID: event.ID})
	delivery := rows.Data[0]
	var retry *bus.RetryAfter
	if err := h.service.HandleDelivery(h.ctx, delivery.ID, false); !errors.As(err, &retry) || retry.Delay != time.Second {
		t.Fatalf("first retry = %#v", err)
	}
	if err := h.service.HandleDelivery(h.ctx, delivery.ID, false); !errors.As(err, &retry) {
		t.Fatalf("early retry was acknowledged: %v", err)
	}
	current, _ := h.service.GetDelivery(h.ctx, delivery.ID)
	if len(current.Attempts) != 1 || current.State != StateRetrying {
		t.Fatalf("early attempt: %+v", current)
	}
	h.now = h.now.Add(2 * time.Second)
	if err := h.service.HandleDelivery(h.ctx, delivery.ID, true); err != nil {
		t.Fatal(err)
	}
	current, _ = h.service.GetDelivery(h.ctx, delivery.ID)
	if current.State != StateSucceeded || len(current.Attempts) != 2 {
		t.Fatalf("retry result: %+v", current)
	}

	goneServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusGone)
	}))
	t.Cleanup(goneServer.Close)
	goneAccount := store.NewID(store.PrefixAccount)
	goneEndpoint := h.endpoint(t, "gone", goneServer.URL, goneAccount)
	goneEvent := h.event(t, goneAccount, "gone")
	if err := h.service.RouteEvent(h.ctx, goneEvent); err != nil {
		t.Fatal(err)
	}
	goneRows, _ := h.service.ListDeliveries(h.ctx, DeliveryFilter{EndpointID: goneEndpoint.ID, EventID: goneEvent.ID})
	if err := h.service.HandleDelivery(h.ctx, goneRows.Data[0].ID, false); err != nil {
		t.Fatal(err)
	}
	goneEndpoint, _ = h.service.GetEndpoint(h.ctx, goneEndpoint.ID)
	if goneEndpoint.Enabled || goneEndpoint.DisabledReason == nil || goneEndpoint.DisabledReason.Code != "http_410" {
		t.Fatalf("410 did not disable: %+v", goneEndpoint)
	}
	if view := endpointView(goneEndpoint); view.DisabledReason == nil || view.DisabledReason.Code != "http_410" {
		t.Fatalf("disabledReason missing from view: %+v", view.DisabledReason)
	}

	breakerAccount := store.NewID(store.PrefixAccount)
	breakerEndpoint := h.endpoint(t, "breaker", retryServer.URL, breakerAccount)
	for range breakerFailureThreshold {
		if err := h.service.breakerRecord(h.ctx, breakerEndpoint, false, false, h.now); err != nil {
			t.Fatal(err)
		}
	}
	breakerEvent := h.event(t, breakerAccount, "breaker")
	if err := h.service.RouteEvent(h.ctx, breakerEvent); err != nil {
		t.Fatal(err)
	}
	breakerRows, _ := h.service.ListDeliveries(h.ctx, DeliveryFilter{EndpointID: breakerEndpoint.ID, EventID: breakerEvent.ID})
	if err := h.service.HandleDelivery(h.ctx, breakerRows.Data[0].ID, false); !errors.As(err, &retry) {
		t.Fatalf("open breaker = %v", err)
	}
	h.now = h.now.Add(breakerInitialOpen)
	if err := h.service.HandleDelivery(h.ctx, breakerRows.Data[0].ID, true); err != nil {
		t.Fatal(err)
	}
	breakerEndpoint, _ = h.service.GetEndpoint(h.ctx, breakerEndpoint.ID)
	if breakerEndpoint.Breaker.State != "closed" {
		t.Fatalf("breaker did not close: %+v", breakerEndpoint.Breaker)
	}

	orderAccount := store.NewID(store.PrefixAccount)
	orderEndpoint := h.endpoint(t, "order", retryServer.URL, orderAccount)
	for _, subject := range []string{"first", "second"} {
		if err := h.service.RouteEvent(h.ctx, h.event(t, orderAccount, subject)); err != nil {
			t.Fatal(err)
		}
	}
	orderRows, _ := h.service.ListDeliveries(h.ctx, DeliveryFilter{EndpointID: orderEndpoint.ID, Sort: "id"})
	if len(orderRows.Data) != 2 {
		t.Fatalf("ordered rows = %d", len(orderRows.Data))
	}
	if err := h.service.HandleDelivery(h.ctx, orderRows.Data[1].ID, false); !errors.As(err, &retry) {
		t.Fatalf("later delivery did not wait: %v", err)
	}
	if err := h.service.HandleDelivery(h.ctx, orderRows.Data[0].ID, false); err != nil {
		t.Fatal(err)
	}
	if err := h.service.HandleDelivery(h.ctx, orderRows.Data[1].ID, false); err != nil {
		t.Fatal(err)
	}

	crashAccount := store.NewID(store.PrefixAccount)
	crashEndpoint := h.endpoint(t, "crash", retryServer.URL, crashAccount)
	crashEvent := h.event(t, crashAccount, "crash")
	if err := h.service.RouteEvent(h.ctx, crashEvent); err != nil {
		t.Fatal(err)
	}
	crashRows, _ := h.service.ListDeliveries(h.ctx, DeliveryFilter{EndpointID: crashEndpoint.ID, EventID: crashEvent.ID})
	_, err := h.db.Collection(DeliveriesCollection).UpdateByID(h.ctx, crashRows.Data[0].ID, bson.M{"$set": bson.M{
		"state": StateDelivering, "updatedAt": h.now.Add(-time.Minute),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.service.HandleDelivery(h.ctx, crashRows.Data[0].ID, true); err != nil {
		t.Fatal(err)
	}
	crashDelivery, _ := h.service.GetDelivery(h.ctx, crashRows.Data[0].ID)
	if crashDelivery.State != StateSucceeded {
		t.Fatalf("crash redelivery = %+v", crashDelivery)
	}

	resolver := staticResolver{
		"blocked.test": {netip.MustParseAddr("127.0.0.1")},
		"rebind.test":  {netip.MustParseAddr("8.8.8.8")},
	}
	h.service.Egress = egress.NewDialer(egress.Policy{BlockPrivate: true})
	h.service.Egress.Resolver = resolver
	rebindAccount := store.NewID(store.PrefixAccount)
	rebindURL := strings.Replace(retryServer.URL, "127.0.0.1", "rebind.test", 1)
	rebindEndpoint := h.endpoint(t, "rebind", rebindURL, rebindAccount)
	resolver["rebind.test"] = []netip.Addr{netip.MustParseAddr("127.0.0.1")}
	rebindEvent := h.event(t, rebindAccount, "rebind")
	if err := h.service.RouteEvent(h.ctx, rebindEvent); err != nil {
		t.Fatal(err)
	}
	rebindRows, _ := h.service.ListDeliveries(h.ctx, DeliveryFilter{EndpointID: rebindEndpoint.ID, EventID: rebindEvent.ID})
	if err := h.service.HandleDelivery(h.ctx, rebindRows.Data[0].ID, false); err != nil {
		t.Fatal(err)
	}
	rebindDelivery, _ := h.service.GetDelivery(h.ctx, rebindRows.Data[0].ID)
	if rebindDelivery.State != StateFailed || rebindDelivery.Attempts[0].Error != string(errs.CodeEndpointURLBlocked) {
		t.Fatalf("DNS rebinding delivery = %+v", rebindDelivery)
	}
	for i, target := range []string{"http://169.254.169.254/latest/meta-data", "http://blocked.test/hook"} {
		if _, err := h.service.CreateEndpoint(h.ctx, EndpointCreate{
			Name: fmt.Sprintf("blocked-%d", i), URL: target, Events: []string{"*"}, Secret: testSecret(byte(22 + i)),
		}); errs.CodeOf(err) != errs.CodeEndpointURLBlocked {
			t.Fatalf("SSRF URL %s accepted: %v", target, err)
		}
	}
}

func TestIntegrationRoutingAndValidationBranches(t *testing.T) {
	h := newWebhookHarness(t)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(receiver.Close)
	accountID := store.NewID(store.PrefixAccount)
	endpoint := h.endpoint(t, "branches", receiver.URL, accountID)
	h.rules.AccountLoader = func(context.Context, string, string) (map[string]any, error) {
		return map[string]any{"name": "account"}, nil
	}
	h.rules.EnvLoader = func(context.Context, string) (map[string]string, []string, error) {
		return map[string]string{"key": "value"}, nil, nil
	}
	badRoute := rules.Route{
		ID: store.NewID(store.PrefixRoute), OrganizationID: h.orgID, Name: "bad-compile",
		Enabled: true, EndpointID: endpoint.ID, CurrentVersion: 1, Filter: "!!!",
		CreatedAt: h.now, UpdatedAt: h.now, Version: 1,
	}
	if _, err := h.db.Collection(rules.RoutesCollection).InsertOne(h.ctx, badRoute); err != nil {
		t.Fatal(err)
	}
	event := h.event(t, accountID, "bad-route")
	if err := h.service.RouteEvent(h.ctx, event); err != nil {
		t.Fatal(err)
	}
	n, err := h.db.Collection(rules.RuleErrorsCollection).CountDocuments(h.ctx, bson.M{
		"organizationId": h.orgID, "routeId": badRoute.ID, "eventId": event.ID,
	})
	if err != nil || n != 1 {
		t.Fatalf("rule errors = %d %v", n, err)
	}

	h.service.RulesEnabled = func(context.Context, string) bool { return false }
	directOnly := h.event(t, accountID, "rules-off")
	if err := h.service.RouteEvent(h.ctx, directOnly); err != nil {
		t.Fatal(err)
	}
	rows, _ := h.service.ListDeliveries(h.ctx, DeliveryFilter{EventID: directOnly.ID})
	if len(rows.Data) != 1 || rows.Data[0].RouteID != "" {
		t.Fatalf("rules switch deliveries = %+v", rows.Data)
	}
	if _, err := h.service.Replay(h.ctx, rows.Data[0].ID); errs.CodeOf(err) != errs.CodeConflict {
		t.Fatalf("pending replay: %v", err)
	}
	if _, err := h.service.ListDeliveries(h.ctx, DeliveryFilter{From: h.now, To: h.now.Add(-time.Second)}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("invalid delivery range: %v", err)
	}
	missingEvent := &Delivery{
		ID: store.NewID(store.PrefixDelivery), OrganizationID: h.orgID,
		EventID: store.NewID(store.PrefixEvent), EventType: events.MessageReceived,
		EndpointID: endpoint.ID, State: StateFailed, IsReplay: true,
		CreatedAt: h.now, UpdatedAt: h.now, ExpireAt: h.now.Add(time.Hour), Version: 1,
	}
	if _, err := h.service.Repo.insertDelivery(h.ctx, missingEvent); err != nil {
		t.Fatal(err)
	}
	if _, err := h.service.Replay(h.ctx, missingEvent.ID); errs.CodeOf(err) != errs.CodeReplayWindowExceeded {
		t.Fatalf("missing event replay: %v", err)
	}
	h.service.RulesEnabled = nil

	publisher := h.service.Publisher
	h.service.Publisher = nil
	if err := h.service.RouteEvent(h.ctx, h.event(t, accountID, "no-publisher")); err == nil {
		t.Fatal("missing publisher accepted")
	}
	h.service.Publisher = publisher

	for _, headers := range [][]WebhookHeader{
		{{Name: "Webhook-Id", Value: "x"}},
		{{Name: "X-A", Value: "x"}, {Name: "x-a", Value: "y"}},
		{{Name: "X-A", Value: "x\nbad"}},
		{{Name: "X-A"}},
	} {
		if _, err := h.service.CreateEndpoint(h.ctx, EndpointCreate{
			Name: store.NewID(store.PrefixEndpoint), URL: receiver.URL,
			Events: []string{"*"}, Headers: headers, Secret: testSecret(41),
		}); errs.CodeOf(err) != errs.CodeValidationFailed {
			t.Errorf("headers %+v: %v", headers, err)
		}
	}
	if _, err := h.service.CreateEndpoint(h.ctx, EndpointCreate{
		Name: "bad-secret", URL: receiver.URL, Events: []string{"*"}, Secret: "bad",
	}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("bad secret: %v", err)
	}

	_, err = h.db.Collection(EndpointsCollection).UpdateByID(h.ctx, endpoint.ID, bson.M{"$set": bson.M{
		"breaker": Breaker{State: "halfOpen", ProbeInFlight: true},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.service.releaseBreakerProbe(h.ctx, endpoint); err != nil {
		t.Fatal(err)
	}
	endpoint, _ = h.service.GetEndpoint(h.ctx, endpoint.ID)
	if endpoint.Breaker.ProbeInFlight {
		t.Fatal("probe was not released")
	}
	secondNode := &Service{Repo: h.service.Repo, Now: h.service.Now}
	allowed := make(chan bool, 2)
	errsCh := make(chan error, 2)
	for _, service := range []*Service{h.service, secondNode} {
		go func() {
			_, ok, _, err := service.breakerAllow(h.ctx, endpoint)
			allowed <- ok
			errsCh <- err
		}()
	}
	allowedCount := 0
	for range 2 {
		if <-allowed {
			allowedCount++
		}
		if err := <-errsCh; err != nil {
			t.Fatal(err)
		}
	}
	if allowedCount != 1 {
		t.Fatalf("half-open probes allowed = %d", allowedCount)
	}
}

func TestIntegrationEmbeddedNATSEventToDelivery(t *testing.T) {
	h := newWebhookHarness(t)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(receiver.Close)
	eventBus, err := bus.Open(h.ctx, bus.Options{Mode: bus.ModeEmbedded, NodeID: "webhook-test", DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(eventBus.Close)
	serverAPI, err := httpapi.New(httpapi.Options{})
	if err != nil {
		t.Fatal(err)
	}
	application := app.New(app.Options{
		Roles:  app.RoleSet{app.RoleAPI: true, app.RoleWebhook: true},
		NodeID: "webhook-test", Log: h.service.Log,
	})
	app.Provide(application, h.db)
	app.Provide(application, h.service.Cipher)
	app.Provide(application, h.service.Egress)
	app.Provide(application, h.service.Events)
	app.Provide(application, eventBus)
	app.Provide(application, h.service.Engine)
	app.Provide(application, h.service.Rules)
	app.Provide(application, h.service.Audit)
	app.Provide(application, serverAPI)
	module := &Module{
		EventPartitions: 1, DeliveryPartitions: 1, Concurrency: 2,
		Log: h.service.Log, SecurityLog: h.service.SecurityLog,
	}
	if err := module.Start(h.ctx, application); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = module.Stop(context.Background()) })
	if module.Name() != "webhook" || len(module.Roles()) != 2 || module.Service() == nil {
		t.Fatal("module identity")
	}
	h.service = module.Service()

	accountID := store.NewID(store.PrefixAccount)
	endpoint := h.endpoint(t, "nats", receiver.URL, accountID)
	event := h.event(t, accountID, "stream")
	raw, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eventBus.Publish(h.ctx, bus.EventSubject(0, h.orgID, event.Type), raw, event.ID, nil); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		rows, listErr := h.service.ListDeliveries(h.ctx, DeliveryFilter{EndpointID: endpoint.ID, EventID: event.ID})
		if listErr == nil && len(rows.Data) == 1 && rows.Data[0].State == StateSucceeded {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("stream delivery did not succeed: %+v %v", rows, listErr)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func mustAuditRows(t *testing.T, h *webhookHarness) []bson.M {
	t.Helper()
	cur, err := h.db.Collection(audit.Collection).Find(h.ctx, bson.M{"organizationId": h.orgID})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cur.Close(h.ctx) }()
	var rows []bson.M
	if err := cur.All(h.ctx, &rows); err != nil {
		t.Fatal(err)
	}
	return rows
}
