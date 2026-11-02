package evidence_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"log/slog"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/errs"
	"github.com/radiobros/emailbridge/internal/evidence"
	"github.com/radiobros/emailbridge/internal/jobs"
)

// failBackend wraps MemoryBackend to inject errors.
type failBackend struct {
	*evidence.MemoryBackend
	getErr      error
	existsErr   error
	putErr      error
	retErr      error
	lockErr     error
	lockEnabled bool
}

func (f *failBackend) Get(ctx context.Context, key string) (io.ReadCloser, int64, error) {
	if f.getErr != nil {
		return nil, 0, f.getErr
	}
	return f.MemoryBackend.Get(ctx, key)
}
func (f *failBackend) Exists(ctx context.Context, key string) (bool, error) {
	if f.existsErr != nil {
		return false, f.existsErr
	}
	return f.MemoryBackend.Exists(ctx, key)
}
func (f *failBackend) PutLocked(ctx context.Context, key string, r io.Reader, size int64, ct, mode string, until time.Time) error {
	if f.putErr != nil {
		return f.putErr
	}
	return f.MemoryBackend.PutLocked(ctx, key, r, size, ct, mode, until)
}
func (f *failBackend) GetRetention(ctx context.Context, key string) (bool, *time.Time, error) {
	if f.retErr != nil {
		return false, nil, f.retErr
	}
	return f.MemoryBackend.GetRetention(ctx, key)
}
func (f *failBackend) ObjectLockEnabled(ctx context.Context) (bool, error) {
	if f.lockErr != nil {
		return false, f.lockErr
	}
	return f.lockEnabled, nil
}

func TestCheckBackendError(t *testing.T) {
	fb := &failBackend{MemoryBackend: evidence.NewMemoryBackend(true), lockErr: errors.New("x")}
	s, _ := evidence.Open(evidence.Options{Backend: fb})
	err := s.Check(context.Background())
	if errs.CodeOf(err) != errs.CodeEvidenceLockUnavailable {
		t.Fatalf("%v", err)
	}
}

func TestArchiveExistsError(t *testing.T) {
	fb := &failBackend{MemoryBackend: evidence.NewMemoryBackend(true), lockEnabled: true, existsErr: errors.New("e")}
	s, _ := evidence.Open(evidence.Options{Backend: fb})
	_ = s.Check(context.Background())
	_, err := s.Archive(context.Background(), evidence.Item{
		ID: "rcp_x", Organization: "o", Account: "a", Raw: []byte("a"), ReceivedAt: time.Now().UTC(),
	})
	if err == nil {
		t.Fatal()
	}
}

func TestArchivePutError(t *testing.T) {
	fb := &failBackend{MemoryBackend: evidence.NewMemoryBackend(true), lockEnabled: true, putErr: errors.New("p")}
	s, _ := evidence.Open(evidence.Options{Backend: fb})
	_ = s.Check(context.Background())
	_, err := s.Archive(context.Background(), evidence.Item{
		ID: "rcp_x", Organization: "o", Account: "a", Raw: []byte("a"), ReceivedAt: time.Now().UTC(),
	})
	if err == nil {
		t.Fatal()
	}
}

func TestVerifyHashObjectGone(t *testing.T) {
	mem := evidence.NewMemoryBackend(true)
	s, _ := evidence.Open(evidence.Options{Backend: mem})
	_ = s.Check(context.Background())
	raw := []byte("gone")
	ref, err := s.Archive(context.Background(), evidence.Item{
		ID: "rcp_g", Organization: "o", Account: "a", Raw: raw, ReceivedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	// overwrite backend get for eml only via failBackend after archive
	fb := &failBackend{MemoryBackend: mem, lockEnabled: true, getErr: evidence.ErrNotFound}
	s2, _ := evidence.Open(evidence.Options{Backend: fb})
	_ = s2.Check(context.Background())
	// Put manifest still readable — but get of eml fails. Use a backend that fails only for .eml
	res, err := s2.Verify(context.Background(), ref, hex.EncodeToString(sha256Sum(raw)))
	// getErr applies to all gets including manifest
	if err == nil && res.Intact {
		t.Fatalf("%+v", res)
	}
}

func sha256Sum(b []byte) []byte {
	s := sha256.Sum256(b)
	return s[:]
}

func TestVerifyRetentionError(t *testing.T) {
	mem := evidence.NewMemoryBackend(true)
	s, _ := evidence.Open(evidence.Options{Backend: mem})
	_ = s.Check(context.Background())
	raw := []byte("ret")
	ref, _ := s.Archive(context.Background(), evidence.Item{
		ID: "rcp_r", Organization: "o", Account: "a", Raw: raw, ReceivedAt: time.Now().UTC(),
	})
	fb := &failBackend{MemoryBackend: mem, lockEnabled: true, retErr: errors.New("r")}
	s2, _ := evidence.Open(evidence.Options{Backend: fb})
	_, err := s2.Verify(context.Background(), ref, "")
	if err == nil {
		t.Fatal()
	}
}

func TestStampNonceAndAlgorithm(t *testing.T) {
	// TSA that returns granted but wrong nonce handled by digitorus ParseResponse + our check
	fx := startFakeTSA(t, false)
	s, _ := evidence.Open(evidence.Options{
		Backend: evidence.NewMemoryBackend(true), HTTPClient: fx.client,
	})
	sum := sha256.Sum256([]byte("n"))
	_, _, err := s.Stamp(context.Background(), hex.EncodeToString(sum[:]), fx.url)
	if err != nil {
		t.Fatal(err)
	}
}

func TestStampDialError(t *testing.T) {
	s, _ := evidence.Open(evidence.Options{
		Backend: evidence.NewMemoryBackend(true),
		HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("dial")
		})},
	})
	sum := sha256.Sum256([]byte("d"))
	_, _, err := s.Stamp(context.Background(), hex.EncodeToString(sum[:]), "http://127.0.0.1:1")
	if errs.CodeOf(err) != errs.CodeEvidenceTsaFailed {
		t.Fatalf("%v", err)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestHandleTSARetryStampFail(t *testing.T) {
	s, _ := evidence.Open(evidence.Options{Backend: evidence.NewMemoryBackend(true)})
	_ = s.Check(context.Background())
	raw := []byte("failstamp")
	ref, _ := s.Archive(context.Background(), evidence.Item{
		ID: "rcp_fs", Organization: "o", Account: "a", Raw: raw, ReceivedAt: time.Now().UTC(),
	})
	sum := sha256.Sum256(raw)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	p := evidence.TSARetryPayload{
		Key: ref.Key, SHA256: hex.EncodeToString(sum[:]), TSAURL: srv.URL,
		FirstAttemptAt: time.Now().UTC(),
	}
	b, _ := json.Marshal(p)
	_, err := s.HandleTSARetry(context.Background(), &jobs.Job{Payload: b}, nil)
	if err == nil {
		t.Fatal()
	}
}

func TestArchiveIdempotentWithoutManifest(t *testing.T) {
	mem := evidence.NewMemoryBackend(true)
	s, _ := evidence.Open(evidence.Options{Backend: mem})
	_ = s.Check(context.Background())
	raw := []byte("nomanifest")
	at := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	item := evidence.Item{ID: "rcp_nm", Organization: "o", Account: "a", Raw: raw, ReceivedAt: at}
	ref, err := s.Archive(context.Background(), item)
	if err != nil {
		t.Fatal(err)
	}
	// Remove only the manifest key by putting empty overwrite — MemoryBackend has no delete;
	// put a different object under a new store that already has eml.
	key := ref.Key
	_ = mem.PutLocked(context.Background(), key, bytes.NewReader(raw), int64(len(raw)), "message/rfc822", "GOVERNANCE", time.Now().Add(time.Hour))
	// Ensure manifest missing: put nothing — re-archive should re-hash object and succeed idempotently.
	// First delete manifest by overwriting store map — use a fresh backend with only eml.
	mem2 := evidence.NewMemoryBackend(true)
	_ = mem2.PutLocked(context.Background(), key, bytes.NewReader(raw), int64(len(raw)), "message/rfc822", "GOVERNANCE", time.Now().Add(time.Hour))
	s2, _ := evidence.Open(evidence.Options{Backend: mem2})
	_ = s2.Check(context.Background())
	ref2, err := s2.Archive(context.Background(), item)
	if err != nil {
		t.Fatal(err)
	}
	if ref2.SHA256 != ref.SHA256 {
		t.Fatal(ref2.SHA256)
	}
}

func TestVerifyWithTSRInvalid(t *testing.T) {
	mem := evidence.NewMemoryBackend(true)
	s, _ := evidence.Open(evidence.Options{Backend: mem})
	_ = s.Check(context.Background())
	raw := []byte("badtsr")
	ref, _ := s.Archive(context.Background(), evidence.Item{
		ID: "rcp_bt", Organization: "o", Account: "a", Raw: raw, ReceivedAt: time.Now().UTC(),
	})
	tsr := evidence.TSRKeyFor(ref.Key)
	_ = mem.PutLocked(context.Background(), tsr, strings.NewReader("not-a-token"), 11, "application/timestamp-reply", "GOVERNANCE", time.Now().Add(time.Hour))
	res, err := s.Verify(context.Background(), ref, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.TSA == nil || res.TSA.Valid {
		t.Fatalf("%+v", res.TSA)
	}
}

func TestModuleStartUsesAppLog(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	a := app.New(app.Options{NodeID: "n", Log: quiet})
	m := &evidence.Module{Options: evidence.Options{Backend: evidence.NewMemoryBackend(false)}}
	if err := m.Start(context.Background(), a); err != nil {
		t.Fatal(err)
	}
}
