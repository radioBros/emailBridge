//go:build integration

package gateways

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/audit"
	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/internal/tenancy"
)

func TestIntegrationSESWebhookConfirmAndNotify(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	_ = Migration(42).Up(ctx, db)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	org := store.NewID(store.PrefixOrganization)
	cctx := tenancy.WithCaller(store.WithOrganization(ctx, org), tenancy.Caller{
		Kind: tenancy.CallerUser, UserID: store.NewID(store.PrefixUser), OrganizationID: org, Role: tenancy.RoleOwner,
	})
	sink := &fakeSink{}

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(3), Subject: pkix.Name{CommonName: "sns"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	confirmHit := false
	mux := http.NewServeMux()
	mux.HandleFunc("/cert.pem", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(pemBytes) })
	mux.HandleFunc("/confirm", func(w http.ResponseWriter, _ *http.Request) { confirmHit = true; w.WriteHeader(http.StatusOK) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		u := *req.URL
		if u.Path == "" || u.Path == "/" {
			u.Path = "/cert.pem"
		}
		u.Scheme = "http"
		u.Host = srv.Listener.Addr().String()
		req2 := req.Clone(req.Context())
		req2.URL = &u
		req2.Host = u.Host
		return http.DefaultTransport.RoundTrip(req2)
	})}

	svc := &Service{
		Repo: &Repo{DB: db}, Cipher: testCipher(t, db),
		Audit: &audit.Writer{DB: db, Log: quiet, Retention: time.Hour},
		Sink:  sink, HTTP: client, limiter: newCallbackLimiter(60),
	}
	g, err := svc.Create(cctx, CreateInput{
		Name: "ses-wh", Kind: KindSES,
		Credentials: &CredentialsInput{AccessKey: "A", SecretKey: "S"},
		API:         &APIConfig{Region: "us-east-1"},
		MaxAttempts: intPtr(5), BackoffBase: "2s",
	})
	if err != nil {
		t.Fatal(err)
	}
	r, err := svc.ResolveAPI(ctx, org, g.ID)
	if err != nil || r.MaxAttempts != 5 || r.BackoffBase != 2*time.Second {
		t.Fatalf("resolve: %v %+v", err, r)
	}

	sign := func(env *snsEnvelope) {
		sts := snsStringToSign(*env)
		sum := sha1.Sum([]byte(sts))
		sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA1, sum[:])
		if err != nil {
			t.Fatal(err)
		}
		env.Signature = base64.StdEncoding.EncodeToString(sig)
		env.SigningCertURL = "https://sns.us-east-1.amazonaws.com/cert.pem"
	}

	// SubscriptionConfirmation
	conf := snsEnvelope{
		Type: "SubscriptionConfirmation", Message: "confirm", MessageId: "1",
		SubscribeURL: srv.URL + "/confirm", Timestamp: "2020-01-01T00:00:00.000Z",
		Token: "tok", TopicArn: "arn:aws:sns:us-east-1:1:t",
	}
	sign(&conf)
	body, _ := json.Marshal(conf)
	if err := svc.HandleDeliveryWebhook(ctx, g.ID, httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))); err != nil {
		t.Fatal(err)
	}
	if !confirmHit {
		t.Fatal("confirm not hit")
	}

	// Notification → sink
	msg, _ := json.Marshal(map[string]any{
		"eventType": "Delivery",
		"mail":      map[string]any{"messageId": "m1"},
		"delivery":  map[string]any{"recipients": []string{"a@b.c"}},
	})
	notif := snsEnvelope{
		Type: "Notification", Message: string(msg), MessageId: "2",
		Timestamp: "2020-01-01T00:00:00.000Z", TopicArn: "arn:aws:sns:us-east-1:1:t",
	}
	sign(&notif)
	body2, _ := json.Marshal(notif)
	if err := svc.HandleDeliveryWebhook(ctx, g.ID, httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body2))); err != nil {
		t.Fatal(err)
	}
	sink.mu.Lock()
	n := len(sink.events)
	sink.mu.Unlock()
	if n < 1 {
		t.Fatal("no events")
	}

	// oversized body
	big := bytes.Repeat([]byte("x"), maxWebhookBody+10)
	if err := svc.HandleDeliveryWebhook(ctx, g.ID, httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(big))); err == nil {
		t.Fatal("expected too large")
	}
}
