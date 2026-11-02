//go:build integration

package gateways

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

type fakeSink struct {
	mu     sync.Mutex
	events []DeliveryEvent
	org    string
	gwy    string
}

func (f *fakeSink) Deliver(_ context.Context, orgID, gatewayID string, events []DeliveryEvent) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.org, f.gwy = orgID, gatewayID
	f.events = append(f.events, events...)
	return nil
}

func TestIntegrationAPIGatewayCRUDResolveWebhook(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	if err := Migration(42).Up(ctx, db); err != nil {
		t.Fatal(err)
	}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	org := store.NewID(store.PrefixOrganization)
	cctx := tenancy.WithCaller(store.WithOrganization(ctx, org), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: store.NewID(store.PrefixUser), OrganizationID: org, Role: tenancy.RoleOwner,
	})
	sink := &fakeSink{}
	svc := &Service{
		Repo: &Repo{DB: db}, Cipher: testCipher(t, db), Egress: egress.NewDialer(egress.Policy{}),
		Audit:        &audit.Writer{DB: db, Log: quiet, Retention: time.Hour},
		CallbackBase: "https://eb.example", Sink: sink, limiter: newCallbackLimiter(60),
	}

	g, err := svc.Create(cctx, CreateInput{
		Name: "mg-api", Kind: KindMailgun,
		API:         &APIConfig{Domain: "ex.com", From: "from@ex.com"},
		Credentials: &CredentialsInput{APIKey: "key-1", WebhookSecret: "whsec"},
	})
	if err != nil {
		t.Fatal(err)
	}
	view := svc.ViewOf(g)
	if view.Kind != KindMailgun || !view.Credentials.HasAPIKey || view.WebhookURL == "" {
		t.Fatalf("view: %+v", view)
	}
	if view.Host != "" || view.Auth != nil {
		t.Fatalf("smtp leaked: %+v", view)
	}

	// kind immutable
	badKind := KindSES
	if _, err := svc.Patch(cctx, g.ID, formatETag(g.Version), PatchInput{Kind: &badKind}); errs.CodeOf(err) != errs.CodeGatewayKindImmutable {
		t.Fatalf("kind change: %v", err)
	}
	// patch API fields + credentials
	newFrom := "other@ex.com"
	api := &APIConfig{Domain: "ex.com", From: newFrom, Region: "eu"}
	creds := &CredentialsInput{WebhookSecret: "whsec2"}
	patched, err := svc.Patch(cctx, g.ID, formatETag(g.Version), PatchInput{API: api, Credentials: creds})
	if err != nil || patched.API == nil || patched.API.From != newFrom {
		t.Fatalf("patch api: %v %+v", err, patched)
	}
	g = patched
	// refuse SMTP fields on API
	host := "smtp.example"
	if _, err := svc.Patch(cctx, g.ID, formatETag(g.Version), PatchInput{Host: &host}); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("smtp on api: %v", err)
	}
	list, err := svc.List(cctx, ListFilter{})
	if err != nil || len(list.Data) < 1 || list.Data[0].WebhookURL == "" {
		t.Fatalf("list webhook: %v %+v", err, list)
	}

	// ResolveSMTP → ErrAPIGateway
	_, err = svc.ResolveSMTP(ctx, org, g.ID)
	if err == nil || !isErrAPIGateway(err) {
		t.Fatalf("ResolveSMTP: %v", err)
	}
	r, err := svc.ResolveAPI(ctx, org, g.ID)
	if err != nil || r.Config.APIKey != "key-1" || r.Config.Domain != "ex.com" {
		t.Fatalf("ResolveAPI: %v %+v", err, r)
	}

	// encryption at rest: raw doc has no plaintext
	raw, err := svc.Repo.get(cctx, g.ID)
	if err != nil || raw.APICreds == nil || raw.APICreds.APIKey.IsZero() {
		t.Fatalf("stored: %v %+v", err, raw)
	}

	// Send via mock provider HTTP
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "<mg-99>"})
	}))
	defer srv.Close()
	r.Config.BaseURL = srv.URL
	res, err := svc.Send(ctx, r, OutboundMail{From: "from@ex.com", To: []string{"a@b.c"}, Subject: "hi", Text: "t"})
	if err != nil || res.ProviderMessageID != "mg-99" {
		t.Fatalf("send: %v %+v", err, res)
	}

	// webhook → sink (secret updated to whsec2 by patch above)
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	token := "tok"
	mac := hmac.New(sha256.New, []byte("whsec2"))
	_, _ = mac.Write([]byte(ts + token))
	sig := hex.EncodeToString(mac.Sum(nil))
	body, _ := json.Marshal(map[string]any{
		"signature":  map[string]string{"timestamp": ts, "token": token, "signature": sig},
		"event-data": map[string]any{"event": "delivered", "recipient": "a@b.c", "id": "mg-99"},
	})
	req := httptest.NewRequest(http.MethodPost, "/callbacks/gateways/"+g.ID, bytes.NewReader(body))
	if err := svc.HandleDeliveryWebhook(ctx, g.ID, req); err != nil {
		t.Fatal(err)
	}
	sink.mu.Lock()
	n := len(sink.events)
	sink.mu.Unlock()
	if n != 1 {
		t.Fatalf("sink events=%d", n)
	}

	// bad signature → 401
	badBody, _ := json.Marshal(map[string]any{
		"signature": map[string]string{"timestamp": ts, "token": token, "signature": "deadbeef"},
	})
	req2 := httptest.NewRequest(http.MethodPost, "/callbacks/gateways/"+g.ID, bytes.NewReader(badBody))
	if err := svc.HandleDeliveryWebhook(ctx, g.ID, req2); errs.CodeOf(err) != errs.CodeUnauthenticated {
		t.Fatalf("bad sig: %v", err)
	}

	// module mounts route
	mod := &Module{CallbackURL: "https://eb.example", Sink: sink, Log: quiet}
	mod.svc = svc
	rtr := chi.NewRouter()
	rtr.Post("/callbacks/gateways/{gatewayId}", mod.rawGatewayWebhook)
	rec := httptest.NewRecorder()
	rtr.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/callbacks/gateways/"+g.ID, bytes.NewReader(body)))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("route status=%d body=%s", rec.Code, rec.Body.String())
	}

	// Postmark + Brevo gateways + query/bearer token webhooks
	pm, err := svc.Create(cctx, CreateInput{
		Name: "pm", Kind: KindPostmark, Credentials: &CredentialsInput{APIKey: "pm", WebhookSecret: "pmtok"},
	})
	if err != nil {
		t.Fatal(err)
	}
	pmBody := []byte(`{"RecordType":"Delivery","MessageID":"1","Recipient":"a@b.c"}`)
	pmReq := httptest.NewRequest(http.MethodPost, "/callbacks/gateways/"+pm.ID+"?token=pmtok", bytes.NewReader(pmBody))
	if err := svc.HandleDeliveryWebhook(ctx, pm.ID, pmReq); err != nil {
		t.Fatal(err)
	}
	bv, err := svc.Create(cctx, CreateInput{
		Name: "bv", Kind: KindBrevo, Credentials: &CredentialsInput{APIKey: "bv", WebhookSecret: "bvtok"},
	})
	if err != nil {
		t.Fatal(err)
	}
	bvReq := httptest.NewRequest(http.MethodPost, "/callbacks/gateways/"+bv.ID, bytes.NewReader([]byte(`{"event":"delivered","email":"a@b.c"}`)))
	bvReq.Header.Set("Authorization", "Bearer bvtok")
	if err := svc.HandleDeliveryWebhook(ctx, bv.ID, bvReq); err != nil {
		t.Fatal(err)
	}

	// SES create + disabled → unavailable; ResolveAPI on SMTP fails
	ses, err := svc.Create(cctx, CreateInput{
		Name: "ses1", Kind: KindSES,
		API:         &APIConfig{Region: "us-east-1"},
		Credentials: &CredentialsInput{AccessKey: "AK", SecretKey: "SK", WebhookSecret: "ignored"},
	})
	if err != nil {
		t.Fatal(err)
	}
	off := false
	ses, err = svc.Patch(cctx, ses.ID, formatETag(ses.Version), PatchInput{Enabled: &off})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ResolveAPI(ctx, org, ses.ID); errs.CodeOf(err) != errs.CodeGatewayUnavailable {
		t.Fatalf("disabled api: %v", err)
	}
	smtpG, err := svc.Create(cctx, CreateInput{
		Name: "smtp1", Host: "127.0.0.1", Port: 25, TLS: TLSNone, Auth: AuthInput{User: "u", Pass: "p"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ResolveAPI(ctx, org, smtpG.ID); errs.CodeOf(err) != errs.CodeGatewayUnavailable {
		t.Fatalf("smtp as api: %v", err)
	}
	if _, err := svc.Verify(cctx, g.ID); errs.CodeOf(err) != errs.CodeValidationFailed {
		t.Fatalf("verify api: %v", err)
	}
	// webhook without sink
	svc.Sink = nil
	if err := svc.HandleDeliveryWebhook(ctx, pm.ID, httptest.NewRequest(http.MethodPost, "/?token=pmtok", bytes.NewReader(pmBody))); errs.CodeOf(err) != errs.CodeServiceUnavailable {
		t.Fatalf("no sink: %v", err)
	}
	svc.Sink = sink
	// mailgun without webhookSecret falls back to apiKey
	mg2, err := svc.Create(cctx, CreateInput{
		Name: "mg2", Kind: KindMailgun, API: &APIConfig{Domain: "ex.com"},
		Credentials: &CredentialsInput{APIKey: "fallback-key"},
	})
	if err != nil {
		t.Fatal(err)
	}
	ts2 := strconv.FormatInt(time.Now().Unix(), 10)
	mac2 := hmac.New(sha256.New, []byte("fallback-key"))
	_, _ = mac2.Write([]byte(ts2 + "t"))
	body2, _ := json.Marshal(map[string]any{
		"signature":  map[string]string{"timestamp": ts2, "token": "t", "signature": hex.EncodeToString(mac2.Sum(nil))},
		"event-data": map[string]any{"event": "delivered", "recipient": "a@b.c"},
	})
	if err := svc.HandleDeliveryWebhook(ctx, mg2.ID, httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body2))); err != nil {
		t.Fatal(err)
	}
}

func isErrAPIGateway(err error) bool {
	return errors.Is(err, ErrAPIGateway)
}
