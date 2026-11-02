package evidence_test

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/internal/evidence"
	"github.com/radiobros/emailbridge/internal/jobs"
)

func TestHandleTSARetry(t *testing.T) {
	fx := startFakeTSA(t, false)
	s, err := evidence.Open(evidence.Options{
		Backend:    evidence.NewMemoryBackend(true),
		TSAAnchors: []*x509.Certificate{fx.caCert},
		HTTPClient: fx.client,
		Retention:  24 * time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	_ = s.Check(ctx)

	raw := []byte("retry body")
	sum := sha256.Sum256(raw)
	ref, err := s.Archive(ctx, evidence.Item{
		ID: "rcp_retry", Organization: "org", Account: "acc", Raw: raw,
		ReceivedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	payload := evidence.NewTSARetryPayload(ref, evidence.Item{
		Organization: "org", Account: "acc", ID: "rcp_retry", TSAURL: fx.url,
	}, time.Now().UTC())
	payload.SHA256 = hex.EncodeToString(sum[:])
	b, _ := json.Marshal(payload)
	j := &jobs.Job{Payload: b, CreatedAt: time.Now().UTC()}
	res, err := s.HandleTSARetry(ctx, j, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res == nil {
		t.Fatal("nil result")
	}
	ref.TSA = true
	ref.TSRKey = evidence.TSRKeyFor(ref.Key)
	vr, err := s.Verify(ctx, ref, payload.SHA256)
	if err != nil || vr.TSA == nil || !vr.TSA.Valid {
		t.Fatalf("%v %+v", err, vr)
	}

	res2, err := s.HandleTSARetry(ctx, j, nil)
	if err != nil {
		t.Fatal(err)
	}
	m, _ := res2.(map[string]string)
	if m["skipped"] != "already_stamped" {
		t.Fatalf("%v", res2)
	}
}

func TestHandleTSARetryMaxAge(t *testing.T) {
	s, err := evidence.Open(evidence.Options{Backend: evidence.NewMemoryBackend(true)})
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Check(context.Background())
	payload := evidence.TSARetryPayload{
		Key: "evidence/o/a/2026/01/x.eml", SHA256: hex.EncodeToString(make([]byte, 32)),
		TSAURL: "http://example", FirstAttemptAt: time.Now().UTC().Add(-48 * time.Hour),
	}
	b, _ := json.Marshal(payload)
	res, err := s.HandleTSARetry(context.Background(), &jobs.Job{Payload: b}, nil)
	if err != nil {
		t.Fatal(err)
	}
	m := res.(map[string]string)
	if m["skipped"] != "max_age" {
		t.Fatalf("%v", res)
	}
}
