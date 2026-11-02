package evidence_test

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/hex"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/digitorus/timestamp"

	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/evidence"
)

type tsaFixture struct {
	caCert *x509.Certificate
	url    string
	client *http.Client
}

func startFakeTSA(t *testing.T, wrongImprint bool) *tsaFixture {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Test TSA CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(10, 0, 0),
		IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTpl, caTpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, _ := x509.ParseCertificate(caDER)

	tsaKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tsaTpl := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "Test TSA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(5, 0, 0),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageTimeStamping},
	}
	tsaDER, err := x509.CreateCertificate(rand.Reader, tsaTpl, caCert, &tsaKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	tsaCert, _ := x509.ParseCertificate(tsaDER)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		req, err := timestamp.ParseRequest(body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		imprint := req.HashedMessage
		if wrongImprint {
			imprint = bytes.Repeat([]byte{0xab}, 32)
		}
		ts := &timestamp.Timestamp{
			HashAlgorithm:     crypto.SHA256,
			HashedMessage:     imprint,
			Time:              time.Now().UTC(),
			Policy:            asn1.ObjectIdentifier{1, 2, 3, 4, 1},
			Nonce:             req.Nonce,
			AddTSACertificate: true,
			Certificates:      []*x509.Certificate{caCert},
		}
		resp, err := ts.CreateResponseWithOpts(tsaCert, tsaKey, crypto.SHA256)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/timestamp-reply")
		_, _ = w.Write(resp)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &tsaFixture{caCert: caCert, url: srv.URL, client: srv.Client()}
}

func TestStampAndVerifyWithTSA(t *testing.T) {
	fx := startFakeTSA(t, false)
	s, err := evidence.Open(evidence.Options{
		Backend:    evidence.NewMemoryBackend(true),
		LockMode:   "GOVERNANCE",
		Retention:  24 * time.Hour,
		TSAAnchors: []*x509.Certificate{fx.caCert},
		HTTPClient: fx.client,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := s.Check(ctx); err != nil {
		t.Fatal(err)
	}
	raw := []byte("stamp me")
	sum := sha256.Sum256(raw)
	hexSum := hex.EncodeToString(sum[:])

	ref, err := s.Archive(ctx, evidence.Item{
		ID: "rcp_tsa", Organization: "org", Account: "acc", Raw: raw,
		ReceivedAt: time.Now().UTC(), Type: "accettazione", TSAURL: fx.url,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !ref.TSA || ref.TSRKey == "" {
		t.Fatalf("%+v", ref)
	}
	res, err := s.Verify(ctx, ref, hexSum)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Intact || !res.HashMatches || res.TSA == nil || !res.TSA.Valid {
		t.Fatalf("%+v", res)
	}
}

func TestStampWrongImprint(t *testing.T) {
	fx := startFakeTSA(t, true)
	s, err := evidence.Open(evidence.Options{
		Backend: evidence.NewMemoryBackend(true), TSAAnchors: []*x509.Certificate{fx.caCert},
		HTTPClient: fx.client,
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Check(context.Background())
	raw := []byte("x")
	sum := sha256.Sum256(raw)
	ref, err := s.Archive(context.Background(), evidence.Item{
		ID: "rcp_bad", Organization: "org", Account: "acc", Raw: raw,
		ReceivedAt: time.Now().UTC(), TSAURL: fx.url,
	})
	if err == nil || errs.CodeOf(err) != errs.CodeEvidenceTsaFailed {
		t.Fatalf("want tsa failed, got %v", err)
	}
	if ref.Key == "" || ref.TSA {
		t.Fatalf("%+v", ref)
	}
	res, err := s.Verify(context.Background(), ref, hex.EncodeToString(sum[:]))
	if err != nil {
		t.Fatal(err)
	}
	if res.TSA != nil {
		t.Fatalf("expected null tsa, got %+v", res.TSA)
	}
}

func TestStampUntrustedAnchor(t *testing.T) {
	fx := startFakeTSA(t, false)
	other, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(99), Subject: pkix.Name{CommonName: "Other"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(1, 0, 0),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, _ := x509.CreateCertificate(rand.Reader, tpl, tpl, &other.PublicKey, other)
	otherCA, _ := x509.ParseCertificate(der)

	s, err := evidence.Open(evidence.Options{
		Backend: evidence.NewMemoryBackend(true), TSAAnchors: []*x509.Certificate{otherCA},
		HTTPClient: fx.client,
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Check(context.Background())
	_, err = s.Archive(context.Background(), evidence.Item{
		ID: "rcp_untrusted", Organization: "org", Account: "acc", Raw: []byte("y"),
		ReceivedAt: time.Now().UTC(), TSAURL: fx.url,
	})
	if err == nil || errs.CodeOf(err) != errs.CodeEvidenceTsaFailed {
		t.Fatalf("want untrusted failure, got %v", err)
	}
}

func TestLoadAnchorsPEM(t *testing.T) {
	fx := startFakeTSA(t, false)
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: fx.caCert.Raw})
	certs, err := evidence.LoadAnchorsPEM(pemBytes)
	if err != nil || len(certs) != 1 {
		t.Fatalf("%v %d", err, len(certs))
	}
	_, err = evidence.LoadAnchorsPEM([]byte("not pem"))
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestStampHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)
	s, err := evidence.Open(evidence.Options{Backend: evidence.NewMemoryBackend(true), HTTPClient: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("a"))
	_, _, err = s.Stamp(context.Background(), hex.EncodeToString(sum[:]), srv.URL)
	if err == nil || errs.CodeOf(err) != errs.CodeEvidenceTsaFailed {
		t.Fatalf("%v", err)
	}
}
