//go:build integration

package webhook

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/events"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

func TestEndpointQuotaInstanceLimit(t *testing.T) {
	h := newWebhookHarness(t)
	h.service.QuotaEndpoints = 1
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	acc := store.NewID(store.PrefixAccount)
	_ = h.endpoint(t, "one", srv.URL+"/a", acc)
	_, err := h.service.CreateEndpoint(h.ctx, EndpointCreate{
		Name: "two", URL: srv.URL + "/b", Events: []string{events.MessageReceived},
		Secret: testSecret(9),
	})
	if errs.CodeOf(err) != errs.CodeQuotaEndpointsExceeded {
		t.Fatalf("want quota_endpoints_exceeded, got %v", err)
	}
}

func TestEndpointQuotaOrgLimit(t *testing.T) {
	h := newWebhookHarness(t)
	h.service.QuotaEndpoints = 0 // unlimited instance
	if _, err := h.db.Collection("organizations").InsertOne(h.ctx, map[string]any{
		"_id": h.orgID, "quotas": map[string]any{tenancy.QuotaEndpoints: int64(1)},
	}); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)
	acc := store.NewID(store.PrefixAccount)
	_ = h.endpoint(t, "one", srv.URL+"/a", acc)
	_, err := h.service.CreateEndpoint(h.ctx, EndpointCreate{
		Name: "two", URL: srv.URL + "/b", Events: []string{events.MessageReceived},
		Secret: testSecret(9),
	})
	if errs.CodeOf(err) != errs.CodeOrganizationQuotaExceeded {
		t.Fatalf("want organization_quota_exceeded, got %v", err)
	}
}
