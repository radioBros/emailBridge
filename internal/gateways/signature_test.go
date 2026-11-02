package gateways

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"strconv"
	"testing"
	"time"
)

func TestMailgunSignatureValidTamperedReplay(t *testing.T) {
	secret := "whsec-mailgun"
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	token := "tok123"
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(ts + token))
	sig := hex.EncodeToString(mac.Sum(nil))
	body, _ := json.Marshal(map[string]any{
		"signature":  map[string]string{"timestamp": ts, "token": token, "signature": sig},
		"event-data": map[string]any{"event": "delivered", "recipient": "a@b.c"},
	})
	ok, _, _ := verifyMailgun(secret, body, time.Now())
	if !ok {
		t.Fatal("valid")
	}
	// tampered
	bad := append([]byte{}, body...)
	bad[len(bad)-2] = 'X'
	ok, _, _ = verifyMailgun(secret, bad, time.Now())
	if ok {
		t.Fatal("tampered should fail")
	}
	// replay
	oldTS := strconv.FormatInt(time.Now().Add(-time.Hour).Unix(), 10)
	mac2 := hmac.New(sha256.New, []byte(secret))
	_, _ = mac2.Write([]byte(oldTS + token))
	oldSig := hex.EncodeToString(mac2.Sum(nil))
	oldBody, _ := json.Marshal(map[string]any{
		"signature": map[string]string{"timestamp": oldTS, "token": token, "signature": oldSig},
	})
	ok, _, _ = verifyMailgun(secret, oldBody, time.Now())
	if ok {
		t.Fatal("replay should fail")
	}
}

func TestSendGridSignatureValidTamperedReplay(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	secret := base64.StdEncoding.EncodeToString(der)
	body := []byte(`[{"event":"delivered","email":"a@b.c"}]`)
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	sum := sha256.Sum256(append([]byte(ts), body...))
	sigDER, err := ecdsa.SignASN1(rand.Reader, key, sum[:])
	if err != nil {
		t.Fatal(err)
	}
	h := http.Header{}
	h.Set("X-Twilio-Email-Event-Webhook-Signature", base64.StdEncoding.EncodeToString(sigDER))
	h.Set("X-Twilio-Email-Event-Webhook-Timestamp", ts)
	if !verifySendGrid(secret, h, body, time.Now()) {
		t.Fatal("valid")
	}
	h.Set("X-Twilio-Email-Event-Webhook-Signature", base64.StdEncoding.EncodeToString([]byte("nope")))
	if verifySendGrid(secret, h, body, time.Now()) {
		t.Fatal("tampered")
	}
	h.Set("X-Twilio-Email-Event-Webhook-Signature", base64.StdEncoding.EncodeToString(sigDER))
	h.Set("X-Twilio-Email-Event-Webhook-Timestamp", strconv.FormatInt(time.Now().Add(-time.Hour).Unix(), 10))
	if verifySendGrid(secret, h, body, time.Now()) {
		t.Fatal("replay")
	}
	// PEM form
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
	h.Set("X-Twilio-Email-Event-Webhook-Timestamp", ts)
	h.Set("X-Twilio-Email-Event-Webhook-Signature", base64.StdEncoding.EncodeToString(sigDER))
	if !verifySendGrid(string(pemBytes), h, body, time.Now()) {
		t.Fatal("pem")
	}
}

func TestPostmarkBrevoToken(t *testing.T) {
	ok, _, _ := verifyProviderSignature(KindPostmark, "sec", nil, "sec", nil, time.Now())
	if !ok {
		t.Fatal("postmark")
	}
	ok, _, _ = verifyProviderSignature(KindPostmark, "sec", nil, "bad", nil, time.Now())
	if ok {
		t.Fatal("postmark bad")
	}
	h := http.Header{}
	h.Set("Authorization", "Bearer sec")
	ok, _, _ = verifyProviderSignature(KindBrevo, "sec", h, "", nil, time.Now())
	if !ok {
		t.Fatal("brevo bearer")
	}
}

func TestSNSCertHost(t *testing.T) {
	if !snsCertHostOK("https://sns.us-east-1.amazonaws.com/SimpleNotificationService-x.pem") {
		t.Fatal("ok host")
	}
	if snsCertHostOK("https://evil.example/x.pem") {
		t.Fatal("evil")
	}
	if snsCertHostOK("http://sns.us-east-1.amazonaws.com/x.pem") {
		t.Fatal("http")
	}
}

func TestClassifySendErr(t *testing.T) {
	e := classifySendErr(apiErr("x", 401, []byte("no")))
	if e == nil {
		t.Fatal("nil")
	}
}
