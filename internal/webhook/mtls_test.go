package webhook

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/crypto"
	"github.com/radiobros/emailbridge/internal/egress"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/events"
)

func testPEMPair(t *testing.T) (certPEM, keyPEM string) {
	t.Helper()
	return testPEMPairExt(t, false)
}

func testServerPEMPair(t *testing.T) (certPEM, keyPEM string) {
	t.Helper()
	return testPEMPairExt(t, true)
}

func testPEMPairExt(t *testing.T, server bool) (certPEM, keyPEM string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: new(big.Int).SetInt64(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "eb-mtls-test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
	}
	if server {
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
		tmpl.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
		tmpl.DNSNames = []string{"localhost"}
	} else {
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	b, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM = string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: b}))
	return certPEM, keyPEM
}

func TestValidatePEMTLS(t *testing.T) {
	cert, key := testPEMPair(t)
	if err := validatePEMTLS(cert, key, ""); err != nil {
		t.Fatal(err)
	}
	if errs.CodeOf(validatePEMTLS(cert, "not-a-key", "")) != errs.CodeValidationFailed {
		t.Fatal("bad key")
	}
	if errs.CodeOf(validatePEMTLS(cert, key, "not-pem")) != errs.CodeValidationFailed {
		t.Fatal("bad ca")
	}
}

func TestMTLSDisabledRejectsTLS(t *testing.T) {
	svc := &Service{Egress: egress.NewDialer(egress.Policy{})}
	ep := &Endpoint{
		Name: "h", Kind: KindHTTP, URL: "http://127.0.0.1:9/h",
		Events:   []string{events.MessageReceived},
		Security: SecurityOptions{SignatureMode: SignatureStandard, TLS: &EndpointTLS{ClientCert: "x"}},
		Retry:    RetryOptions{MaxAttempts: 1, BackoffBase: "1s", Timeout: "1s"},
		Payload:  PayloadOptions{Text: "none"},
		Delivery: DeliveryOptions{
			Ordering: OrderingStrict, ContentType: "json",
			MaxConcurrency: 8, AutoDisableAfter: "120h",
		},
	}
	if errs.CodeOf(svc.validateEndpoint(context.Background(), ep)) != errs.CodeFeatureDisabled {
		t.Fatal("expected feature_disabled")
	}
}

func TestTLSViewRedactsKey(t *testing.T) {
	v := tlsView(&EndpointTLS{ClientCert: "CERT", ClientKey: crypto.EncryptedString{DEK: "d", CT: []byte("c")}, CABundle: "CA"})
	if v == nil || v.ClientKey != sinkSecretMarker || v.ClientCert != "CERT" {
		t.Fatalf("%+v", v)
	}
	if tlsView(nil) != nil {
		t.Fatal("nil")
	}
}

func TestHTTPClientForPlain(t *testing.T) {
	svc := &Service{Egress: egress.NewDialer(egress.Policy{})}
	c, err := svc.httpClientFor(&Endpoint{ID: "whe_x", Version: 1}, time.Second)
	if err != nil || c == nil {
		t.Fatal(err)
	}
}

func TestEncryptTLSRoundTrip(t *testing.T) {
	cert, key := testPEMPair(t)
	local, err := crypto.NewLocal([]crypto.LocalKey{{ID: "t", Key: make([]byte, 32)}}, "")
	if err != nil {
		t.Fatal(err)
	}
	storeMem := &testKeyStore{keys: map[string]crypto.DataKey{}}
	var seq int
	svc := &Service{Cipher: &crypto.Cipher{
		KEK: local, Store: storeMem,
		NewID: func() string { seq++; return fmt.Sprintf("dek_%026d", seq) },
	}, Egress: egress.NewDialer(egress.Policy{})}
	ep := &Endpoint{ID: "whe_tls", OrganizationID: "org_x"}
	enc, err := svc.encryptTLS(context.Background(), ep, &TLSInput{ClientCert: cert, ClientKey: key}, nil)
	if err != nil || enc == nil || enc.ClientKey.IsZero() {
		t.Fatalf("%+v %v", enc, err)
	}
	kept, err := svc.encryptTLS(context.Background(), ep, &TLSInput{ClientCert: cert, ClientKey: sinkSecretMarker}, enc)
	if err != nil || kept.ClientKey.DEK != enc.ClientKey.DEK {
		t.Fatalf("keep key: %+v %v", kept, err)
	}
	if _, err := svc.encryptTLS(context.Background(), ep, &TLSInput{ClientCert: cert, ClientKey: "bad"}, nil); err == nil {
		t.Fatal("bad key")
	}
}
