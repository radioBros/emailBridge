//go:build integration

package webhook

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
)

func TestIntegrationEndpointSearchAndLastDelivery(t *testing.T) {
	h := newWebhookHarness(t)
	if err := LastDeliveryIndexMigration(1).Up(h.ctx, h.db); err != nil {
		t.Fatal(err)
	}
	srvAlpha := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srvAlpha.Close)
	srvSpecial := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srvSpecial.Close)
	srvNoise := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srvNoise.Close)

	epName, err := h.service.CreateEndpoint(h.ctx, EndpointCreate{
		Name: "Alpha Hook", URL: srvAlpha.URL + "/alpha", Events: []string{"*"}, Secret: testSecret(1),
	})
	if err != nil {
		t.Fatal(err)
	}
	epURL, err := h.service.CreateEndpoint(h.ctx, EndpointCreate{
		Name: "Other", URL: srvSpecial.URL + "/special-path", Events: []string{"*"}, Secret: testSecret(2),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.service.CreateEndpoint(h.ctx, EndpointCreate{
		Name: "Noise", URL: srvNoise.URL + "/x", Events: []string{"*"}, Secret: testSecret(3),
	})
	if err != nil {
		t.Fatal(err)
	}

	byName, err := h.service.ListEndpoints(h.ctx, EndpointListFilter{Q: "alpha", Page: 1, Limit: 10, IncludeTotal: true})
	if err != nil || len(byName.Data) != 1 || byName.Data[0].ID != epName.ID || byName.Total == nil || *byName.Total != 1 {
		t.Fatalf("name q: %+v %v", byName, err)
	}
	byURL, err := h.service.ListEndpoints(h.ctx, EndpointListFilter{Q: "special-path", Limit: 10})
	if err != nil || len(byURL.Data) != 1 || byURL.Data[0].ID != epURL.ID {
		t.Fatalf("url q: %+v %v", byURL, err)
	}
	literal, err := h.service.ListEndpoints(h.ctx, EndpointListFilter{Q: ".*", Limit: 10})
	if err != nil || len(literal.Data) != 0 {
		t.Fatalf("regex metachar must match literally: %+v %v", literal, err)
	}
	if _, err := h.service.ListEndpoints(h.ctx, EndpointListFilter{Q: string(make([]byte, 201))}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("long q: %v", err)
	}

	view := h.service.ViewEndpoint(h.ctx, epName)
	if view.LastDelivery != nil {
		t.Fatalf("no deliveries yet: %+v", view.LastDelivery)
	}

	attemptAt := h.now.Add(time.Minute)
	_, err = h.db.Collection(DeliveriesCollection).InsertOne(h.ctx, Delivery{
		ID: store.NewID(store.PrefixDelivery), OrganizationID: h.orgID, EndpointID: epName.ID,
		EventID: "evt_0", EventType: "message.received", State: StateFailed,
		CreatedAt: h.now.Add(-time.Hour), UpdatedAt: h.now, ExpireAt: h.now.Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.db.Collection(DeliveriesCollection).InsertOne(h.ctx, Delivery{
		ID: store.NewID(store.PrefixDelivery), OrganizationID: h.orgID, EndpointID: epName.ID,
		EventID: "evt_1", EventType: "message.received", State: StateSucceeded,
		Attempts:  []DeliveryAttempt{{At: attemptAt, StatusCode: 204, LatencyMs: 12}},
		CreatedAt: h.now, UpdatedAt: h.now, ExpireAt: h.now.Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}

	listed, err := h.service.ListEndpoints(h.ctx, EndpointListFilter{Q: "Alpha", Limit: 10})
	if err != nil || len(listed.Data) != 1 || listed.Data[0].LastDelivery == nil {
		t.Fatalf("lastDelivery on list: %+v %v", listed, err)
	}
	ld := listed.Data[0].LastDelivery
	if ld.State != StateSucceeded || ld.StatusCode != http.StatusNoContent || !ld.At.Equal(attemptAt) {
		t.Fatalf("lastDelivery=%+v", ld)
	}
	got := h.service.ViewEndpoint(h.ctx, epURL)
	if got.LastDelivery != nil {
		t.Fatalf("endpoint without deliveries must omit lastDelivery: %+v", got.LastDelivery)
	}
}

func TestIntegrationDeliveryOptionsCreatePatch(t *testing.T) {
	h := newWebhookHarness(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	created, err := h.service.CreateEndpoint(h.ctx, EndpointCreate{
		Name: "delivery-opts", URL: srv.URL, Events: []string{"*"}, Secret: testSecret(11),
	})
	if err != nil {
		t.Fatal(err)
	}
	view := h.service.ViewEndpoint(h.ctx, created)
	if view.Delivery.Ordering != OrderingStrict || view.Delivery.MaxConcurrency != h.service.Concurrency ||
		view.Delivery.ContentType != "json" || view.Delivery.RetryOn4xx || view.Delivery.AutoDisableAfter == "" {
		t.Fatalf("defaults: %+v (node concurrency=%d)", view.Delivery, h.service.Concurrency)
	}
	if view.DisabledReason != nil {
		t.Fatalf("unexpected disabledReason: %+v", view.DisabledReason)
	}

	ordering := OrderingBestEffort
	maxC := 2
	content := "cloudevents"
	retry4xx := true
	auto := "48h"
	patched, err := h.service.UpdateEndpoint(h.ctx, created.ID, EndpointUpdate{
		Delivery: &DeliveryUpdate{
			Ordering: &ordering, MaxConcurrency: &maxC, ContentType: &content,
			RetryOn4xx: &retry4xx, AutoDisableAfter: &auto,
		},
	}, formatETag(created.Version))
	if err != nil {
		t.Fatal(err)
	}
	pv := h.service.ViewEndpoint(h.ctx, patched)
	if pv.Delivery.Ordering != OrderingBestEffort || pv.Delivery.MaxConcurrency != 2 ||
		pv.Delivery.ContentType != "cloudevents" || !pv.Delivery.RetryOn4xx || pv.Delivery.AutoDisableAfter != "48h" {
		t.Fatalf("patched delivery: %+v", pv.Delivery)
	}

	badMax := 64
	if _, err := h.service.UpdateEndpoint(h.ctx, created.ID, EndpointUpdate{
		Delivery: &DeliveryUpdate{MaxConcurrency: &badMax},
	}, formatETag(patched.Version)); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("maxConcurrency above node: %v", err)
	}
	badOrder := "fifo"
	if _, err := h.service.UpdateEndpoint(h.ctx, created.ID, EndpointUpdate{
		Delivery: &DeliveryUpdate{Ordering: &badOrder},
	}, formatETag(patched.Version)); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("bad ordering: %v", err)
	}
	short := "30m"
	if _, err := h.service.CreateEndpoint(h.ctx, EndpointCreate{
		Name: "bad-auto", URL: srv.URL + "/b", Events: []string{"*"}, Secret: testSecret(12),
		Delivery: &DeliveryUpdate{AutoDisableAfter: &short},
	}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("short autoDisableAfter: %v", err)
	}
}
