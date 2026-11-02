package gateways

import (
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
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
)

func TestVerifySNSMessageAndUnwrap(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "sns"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	certSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(pemBytes)
	}))
	defer certSrv.Close()

	env := snsEnvelope{
		Type: "Notification", Message: `{"eventType":"Delivery","mail":{"messageId":"m1"}}`,
		MessageId: "mid-1", Timestamp: time.Now().UTC().Format(time.RFC3339),
		TopicArn: "arn:aws:sns:us-east-1:1:t", SignatureVersion: "1",
	}
	stringToSign := snsStringToSign(env)
	sum := sha1.Sum([]byte(stringToSign))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA1, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	env.Signature = base64.StdEncoding.EncodeToString(sig)

	if !snsCertHostOK("https://sns.eu-west-1.amazonaws.com/cert.pem") {
		t.Fatal("host")
	}

	payload, isConfirm, err := unwrapSNS([]byte(`{"Type":"SubscriptionConfirmation","SubscribeURL":"https://sns.us-east-1.amazonaws.com/?Action=Confirm"}`))
	if err != nil || !isConfirm || payload != nil {
		t.Fatalf("confirm unwrap: %v %v %v", err, isConfirm, payload)
	}
	inner, isConfirm, err := unwrapSNS([]byte(`{"Type":"Notification","Message":"{\"a\":1}"}`))
	if err != nil || isConfirm || string(inner) != `{"a":1}` {
		t.Fatalf("notif: %v %s", err, inner)
	}

	ok, url, _ := verifySES(nil, []byte(`{"Type":"SubscriptionConfirmation","SubscribeURL":"https://example/confirm","SigningCertURL":"https://sns.us-east-1.amazonaws.com/c.pem","Signature":"x"}`), time.Now())
	if !ok || url == "" {
		t.Fatal("verifySES confirm")
	}
	ok, _, _ = verifySES(nil, []byte(`{"Type":"Notification","SigningCertURL":"https://sns.us-east-1.amazonaws.com/c.pem","Signature":"x"}`), time.Now())
	if !ok {
		t.Fatal("verifySES notif")
	}
	_ = certSrv
	_ = env
}

func TestConfirmSNS(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	svc := &Service{}
	if err := svc.confirmSNS(context.Background(), srv.URL); err != nil {
		t.Fatal(err)
	}
	if err := svc.confirmSNS(context.Background(), "://bad"); errs.CodeOf(err) != errs.CodeUnauthenticated {
		t.Fatalf("bad url: %v", err)
	}
}

func TestVerifySNSMessageOK(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "sns"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})

	mux := http.NewServeMux()
	mux.HandleFunc("/cert.pem", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(pemBytes)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	env := snsEnvelope{
		Type: "Notification", Message: "hello", MessageId: "1",
		Timestamp: "2020-01-01T00:00:00.000Z", TopicArn: "arn:aws:sns:us-east-1:1:t",
	}
	sts := snsStringToSign(env)
	sum := sha1.Sum([]byte(sts))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA1, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	env.Signature = base64.StdEncoding.EncodeToString(sig)
	env.SigningCertURL = "https://sns.us-east-1.amazonaws.com/cert.pem"

	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		req.URL.Scheme = "http"
		req.URL.Host = srv.Listener.Addr().String()
		req.URL.Path = "/cert.pem"
		return http.DefaultTransport.RoundTrip(req)
	})}
	body, _ := json.Marshal(env)
	if err := VerifySNSMessage(context.Background(), client, body); err != nil {
		t.Fatal(err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
