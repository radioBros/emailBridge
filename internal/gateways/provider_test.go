package gateways

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/radiobros/emailbridge/internal/errs"
)

func TestProviderForAndKinds(t *testing.T) {
	for _, k := range []string{KindSES, KindPostmark, KindMailgun, KindSendGrid, KindBrevo} {
		p, ok := ProviderFor(k)
		if !ok || p.Kind() != k {
			t.Fatalf("%s: %+v %v", k, p, ok)
		}
	}
	if _, ok := ProviderFor("smtp"); ok {
		t.Fatal("smtp should not be an API provider")
	}
	if len(APIKinds()) != 5 {
		t.Fatalf("kinds=%v", APIKinds())
	}
}

func TestClassifyAPIError(t *testing.T) {
	cases := []struct {
		status int
		body   string
		kind   errs.Kind
	}{
		{http.StatusUnauthorized, "", errs.KindAuth},
		{http.StatusTooManyRequests, "slow down", errs.KindRateLimited},
		{http.StatusServiceUnavailable, "", errs.KindTemporary},
		{http.StatusBadRequest, "bad", errs.KindPermanent},
		{http.StatusOK, "", "success"},
	}
	for _, c := range cases {
		k, _ := ClassifyAPIError(c.status, c.body)
		if k != c.kind {
			t.Fatalf("%d: got %s want %s", c.status, k, c.kind)
		}
	}
}

func TestPostmarkSendAndWebhook(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Postmark-Server-Token") != "tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"MessageID": "pm-1", "To": "a@b.c"})
	}))
	defer srv.Close()
	p, _ := ProviderFor(KindPostmark)
	res, err := p.Send(context.Background(), srv.Client(), ProviderConfig{APIKey: "tok", BaseURL: srv.URL}, OutboundMail{
		From: "from@ex.com", To: []string{"a@b.c"}, Subject: "hi", Text: "t", OutboundID: "out_1",
	})
	if err != nil || res.ProviderMessageID != "pm-1" {
		t.Fatalf("%+v %v", res, err)
	}
	evs, err := p.ParseWebhook(nil, []byte(`{"RecordType":"Bounce","Type":"HardBounce","MessageID":"pm-1","Email":"a@b.c","Description":"no","Metadata":{"outboundId":"out_1"}}`))
	if err != nil || len(evs) != 1 || evs[0].Type != DeliveryBounced || evs[0].OutboundID != "out_1" {
		t.Fatalf("%+v %v", evs, err)
	}
	evs, _ = p.ParseWebhook(nil, []byte(`{"RecordType":"Delivery","MessageID":"pm-1","Recipient":"a@b.c"}`))
	if evs[0].Type != DeliverySent {
		t.Fatalf("%+v", evs)
	}
	evs, _ = p.ParseWebhook(nil, []byte(`{"RecordType":"SpamComplaint","MessageID":"pm-1","Email":"a@b.c"}`))
	if evs[0].Type != DeliveryComplained {
		t.Fatalf("%+v", evs)
	}
}

func TestMailgunSendAndWebhook(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != "api" || p != "key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if !strings.Contains(r.URL.Path, "/v3/ex.com/messages") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "<mg-1>", "message": "Queued"})
	}))
	defer srv.Close()
	p, _ := ProviderFor(KindMailgun)
	res, err := p.Send(context.Background(), srv.Client(), ProviderConfig{APIKey: "key", Domain: "ex.com", BaseURL: srv.URL}, OutboundMail{
		From: "from@ex.com", To: []string{"a@b.c"}, Subject: "hi", Text: "t", OutboundID: "out_1",
	})
	if err != nil || res.ProviderMessageID != "mg-1" {
		t.Fatalf("%+v %v", res, err)
	}
	body := `{"event-data":{"event":"failed","id":"mg-1","recipient":"a@b.c","severity":"permanent","reason":"bounce","user-variables":{"outboundId":"out_1"}}}`
	evs, err := p.ParseWebhook(nil, []byte(body))
	if err != nil || evs[0].Type != DeliveryBounced || evs[0].OutboundID != "out_1" {
		t.Fatalf("%+v %v", evs, err)
	}
}

func TestSendGridSendAndWebhook(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sg" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"message_id":"sg-1"}`))
	}))
	defer srv.Close()
	p, _ := ProviderFor(KindSendGrid)
	res, err := p.Send(context.Background(), srv.Client(), ProviderConfig{APIKey: "sg", BaseURL: srv.URL}, OutboundMail{
		From: "from@ex.com", To: []string{"a@b.c"}, Subject: "hi", HTML: "<b>x</b>", OutboundID: "out_1",
	})
	if err != nil || res.ProviderMessageID != "sg-1" {
		t.Fatalf("%+v %v", res, err)
	}
	evs, err := p.ParseWebhook(nil, []byte(`[{"event":"bounce","email":"a@b.c","sg_message_id":"sg-1","reason":"x","outboundId":"out_1"}]`))
	if err != nil || evs[0].Type != DeliveryBounced {
		t.Fatalf("%+v %v", evs, err)
	}
	evs, _ = p.ParseWebhook(nil, []byte(`[{"event":"spamreport","email":"a@b.c","sg_message_id":"sg-1"}]`))
	if evs[0].Type != DeliveryComplained {
		t.Fatalf("%+v", evs)
	}
}

func TestBrevoSendAndWebhook(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("api-key") != "br" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"messageId": "br-1"})
	}))
	defer srv.Close()
	p, _ := ProviderFor(KindBrevo)
	res, err := p.Send(context.Background(), srv.Client(), ProviderConfig{APIKey: "br", BaseURL: srv.URL}, OutboundMail{
		From: "from@ex.com", To: []string{"a@b.c"}, Subject: "hi", Text: "t", OutboundID: "out_1",
	})
	if err != nil || res.ProviderMessageID != "br-1" {
		t.Fatalf("%+v %v", res, err)
	}
	evs, err := p.ParseWebhook(nil, []byte(`{"event":"hard_bounce","email":"a@b.c","message-id":"br-1","tag":"out_1","reason":"x"}`))
	if err != nil || evs[0].Type != DeliveryBounced || evs[0].OutboundID != "out_1" {
		t.Fatalf("%+v %v", evs, err)
	}
}

func TestSESSendAndWebhook(t *testing.T) {
	var sawAuth bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		sawAuth = true
		b, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(b), "FromEmailAddress") {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("bad"))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"MessageId": "ses-1"})
	}))
	defer srv.Close()
	p, _ := ProviderFor(KindSES)
	res, err := p.Send(context.Background(), srv.Client(), ProviderConfig{
		AccessKey: "AKIA", SecretKey: "secret", Region: "eu-west-1", BaseURL: srv.URL,
	}, OutboundMail{From: "from@ex.com", To: []string{"a@b.c"}, Subject: "hi", Text: "t", OutboundID: "out_1"})
	if err != nil || res.ProviderMessageID != "ses-1" || !sawAuth {
		t.Fatalf("%+v %v saw=%v", res, err, sawAuth)
	}
	msg, _ := json.Marshal(map[string]any{
		"notificationType": "Bounce",
		"mail":             map[string]any{"messageId": "ses-1", "tags": map[string]any{"eb_outbound_id": []string{"out_1"}}},
		"bounce": map[string]any{
			"bounceType": "Permanent",
			"bouncedRecipients": []map[string]any{
				{"emailAddress": "a@b.c", "diagnosticCode": "550"},
			},
		},
	})
	env, _ := json.Marshal(map[string]any{"Type": "Notification", "Message": string(msg)})
	evs, err := p.ParseWebhook(nil, env)
	if err != nil || len(evs) != 1 || evs[0].Type != DeliveryBounced || evs[0].OutboundID != "out_1" {
		t.Fatalf("%+v %v", evs, err)
	}
	del, _ := json.Marshal(map[string]any{
		"eventType": "Delivery",
		"mail":      map[string]any{"messageId": "ses-1"},
		"delivery":  map[string]any{"recipients": []string{"a@b.c"}},
	})
	evs, _ = p.ParseWebhook(nil, del)
	if evs[0].Type != DeliverySent {
		t.Fatalf("%+v", evs)
	}
}

func TestProviderSendValidation(t *testing.T) {
	p, _ := ProviderFor(KindPostmark)
	_, err := p.Send(context.Background(), nil, ProviderConfig{}, OutboundMail{})
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestProviderAPIErrorsAndWebhooks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(strings.Repeat("rate limit ", 30)))
	}))
	defer srv.Close()
	for _, kind := range []string{KindPostmark, KindSendGrid, KindBrevo} {
		p, _ := ProviderFor(kind)
		cfg := ProviderConfig{APIKey: "k", BaseURL: srv.URL, From: "a@b.c"}
		_, err := p.Send(context.Background(), srv.Client(), cfg, OutboundMail{
			From: "a@b.c", To: []string{"b@c.d"}, Subject: "x", Text: "y",
		})
		if err == nil || !strings.Contains(err.Error(), "rate_limited") && !strings.Contains(err.Error(), "HTTP 429") {
			t.Fatalf("%s: %v", kind, err)
		}
	}
	p, _ := ProviderFor(KindSES)
	evs, err := p.ParseWebhook(nil, []byte(`{"eventType":"Complaint","mail":{"messageId":"m"},"complaint":{"complainedRecipients":[{"emailAddress":"a@b.c"}]}}`))
	if err != nil || evs[0].Type != DeliveryComplained {
		t.Fatalf("%+v %v", evs, err)
	}
	evs, _ = p.ParseWebhook(nil, []byte(`{"eventType":"Reject","mail":{"messageId":"m"}}`))
	if evs[0].Type != DeliveryFailed {
		t.Fatalf("%+v", evs)
	}
	sg, _ := ProviderFor(KindSendGrid)
	evs, _ = sg.ParseWebhook(nil, []byte(`{"event":"delivered","email":"a@b.c","sg_message_id":"1"}`))
	if len(evs) != 1 || evs[0].Type != DeliverySent {
		t.Fatalf("%+v", evs)
	}
	evs, _ = sg.ParseWebhook(nil, []byte(`[{"event":"dropped","email":"a@b.c","sg_message_id":"1","reason":"x"},{"event":"deferred","email":"a@b.c","sg_message_id":"1"}]`))
	if len(evs) != 2 || evs[0].Type != DeliveryFailed || evs[1].Type != DeliveryDeferred {
		t.Fatalf("%+v", evs)
	}
	br, _ := ProviderFor(KindBrevo)
	evs, _ = br.ParseWebhook(nil, []byte(`{"event":"soft_bounce","email":"a@b.c","message-id":"1"}`))
	if evs[0].Type != DeliveryBounced || evs[0].BounceType != "soft" {
		t.Fatalf("%+v", evs)
	}
	evs, _ = br.ParseWebhook(nil, []byte(`{"event":"spam","email":"a@b.c"}`))
	if evs[0].Type != DeliveryComplained {
		t.Fatalf("%+v", evs)
	}
	mg, _ := ProviderFor(KindMailgun)
	evs, _ = mg.ParseWebhook(nil, []byte(`{"event":"delivered","id":"1","recipient":"a@b.c"}`))
	if evs[0].Type != DeliverySent {
		t.Fatalf("%+v", evs)
	}
	evs, _ = mg.ParseWebhook(nil, []byte(`{"event":"complained","id":"1","recipient":"a@b.c"}`))
	if evs[0].Type != DeliveryComplained {
		t.Fatalf("%+v", evs)
	}
}
