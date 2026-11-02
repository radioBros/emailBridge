package evidence

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
)

// ErrNotFound is returned when an evidence object is missing.
var ErrNotFound = errors.New("evidence: not found")

// Options configure a Store.
type Options struct {
	Endpoint  string
	Region    string
	AccessKey string
	SecretKey string
	Bucket    string
	PathStyle bool
	// LockMode is GOVERNANCE (default) or COMPLIANCE (EB_S3_EVIDENCE_LOCK_MODE).
	LockMode string
	// Retention is the default retain duration when Item.Retention is 0.
	Retention time.Duration
	// TSAAnchors are trusted roots for TSA chain verification (EB_PEC_TSA_ANCHORS); empty = skip chain check.
	TSAAnchors []*x509.Certificate
	// HTTPClient is the egress client used for TSA; nil = http.DefaultClient.
	HTTPClient *http.Client
	// Backend overrides the S3 client (tests).
	Backend Backend
}

// Store archives and verifies PEC evidence.
type Store struct {
	backend    Backend
	lockMode   string
	retention  time.Duration
	tsaAnchors []*x509.Certificate
	http       *http.Client

	mu    sync.Mutex
	ready bool
}

// Open builds a Store. Call Check before Archive.
func Open(o Options) (*Store, error) {
	backend := o.Backend
	if backend == nil {
		b, err := NewMinioBackend(o.Endpoint, o.Region, o.AccessKey, o.SecretKey, o.Bucket, o.PathStyle)
		if err != nil {
			return nil, err
		}
		backend = b
	}
	mode := strings.ToUpper(strings.TrimSpace(o.LockMode))
	if mode == "" {
		mode = "GOVERNANCE"
	}
	if mode != "GOVERNANCE" && mode != "COMPLIANCE" {
		return nil, fmt.Errorf("evidence: invalid LockMode %q", o.LockMode)
	}
	ret := o.Retention
	if ret <= 0 {
		ret = DefaultRetention
	}
	hc := o.HTTPClient
	if hc == nil {
		hc = http.DefaultClient
	}
	return &Store{
		backend: backend, lockMode: mode, retention: ret,
		tsaAnchors: o.TSAAnchors, http: hc,
	}, nil
}

// Check verifies the evidence bucket exists and has Object Lock enabled.
func (s *Store) Check(ctx context.Context) error {
	ok, err := s.backend.ObjectLockEnabled(ctx)
	if err != nil {
		s.setReady(false)
		return errs.Wrap(err, errs.CodeEvidenceLockUnavailable, "evidence.Check")
	}
	if !ok {
		s.setReady(false)
		return errs.New(errs.CodeEvidenceLockUnavailable, "Evidence bucket Object Lock is not enabled; archiving refused.")
	}
	s.setReady(true)
	return nil
}

// Ready reports whether Check succeeded.
func (s *Store) Ready() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ready
}

func (s *Store) setReady(v bool) {
	s.mu.Lock()
	s.ready = v
	s.mu.Unlock()
}

// Archive writes raw bytes + manifest with Object Lock. Idempotent on same id+hash.
// When Item.TSAURL is set and stamping fails, the archive still succeeds and the error is retryable
// (evidence_tsa_failed) so the caller can schedule evidence.tsa-retry.
func (s *Store) Archive(ctx context.Context, item Item) (Ref, error) {
	if !s.Ready() {
		return Ref{}, errs.New(errs.CodeEvidenceLockUnavailable, "Evidence archiving refused: Object Lock check has not passed.")
	}
	if item.ID == "" || item.Organization == "" || item.Account == "" {
		return Ref{}, errs.New(errs.CodeValidationFailed, "Evidence item id, organization and account are required.")
	}
	if len(item.Raw) == 0 {
		return Ref{}, errs.New(errs.CodeValidationFailed, "Evidence raw bytes are required.")
	}
	at := item.ReceivedAt.UTC()
	if at.IsZero() {
		at = time.Now().UTC()
	}
	sum := sha256.Sum256(item.Raw)
	hashHex := hex.EncodeToString(sum[:])
	emlKey := ObjectKey(item.Organization, item.Account, item.ID, at)
	manKey := ManifestKeyFor(emlKey)

	exists, err := s.backend.Exists(ctx, emlKey)
	if err != nil {
		return Ref{}, errs.Wrap(err, errs.CodeInternalError, "evidence.Archive")
	}
	if exists {
		existing, err := s.readManifest(ctx, manKey)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return Ref{}, errs.Wrap(err, errs.CodeInternalError, "evidence.Archive")
		}
		if existing != nil && existing.SHA256 == hashHex {
			ref := Ref{Key: emlKey, ManifestKey: manKey, SHA256: hashHex, TSA: existing.TSA != nil}
			if existing.TSA != nil {
				ref.TSRKey = TSRKeyFor(emlKey)
			}
			if locked, until, _ := s.backend.GetRetention(ctx, emlKey); locked {
				ref.LockedUntil = until
			}
			return ref, nil
		}
		// Re-hash object if manifest missing or claims a different hash.
		got, err := s.hashObject(ctx, emlKey)
		if err != nil {
			return Ref{}, errs.Wrap(err, errs.CodeInternalError, "evidence.Archive")
		}
		if got == hashHex {
			ref := Ref{Key: emlKey, ManifestKey: manKey, SHA256: hashHex}
			if locked, until, _ := s.backend.GetRetention(ctx, emlKey); locked {
				ref.LockedUntil = until
			}
			return ref, nil
		}
		return Ref{}, errs.New(errs.CodeEvidenceHashConflict, "Evidence for this id already exists with a different hash.").
			WithSafe("id", item.ID).WithSafe("existingSha256", got)
	}

	ret := item.Retention
	if ret <= 0 {
		ret = s.retention
	}
	until := time.Now().UTC().Add(ret)
	if err := s.backend.PutLocked(ctx, emlKey, bytes.NewReader(item.Raw), int64(len(item.Raw)), "message/rfc822", s.lockMode, until); err != nil {
		return Ref{}, errs.Wrap(err, errs.CodeInternalError, "evidence.Archive.put")
	}
	man := Manifest{
		ID: item.ID, SHA256: hashHex, Size: int64(len(item.Raw)), ReceivedAt: at,
		Account: item.Account, Type: item.Type, TransportID: item.TransportID, Signature: item.Signature,
	}
	ref := Ref{Key: emlKey, ManifestKey: manKey, SHA256: hashHex, LockedUntil: &until}

	var stampErr error
	if item.TSAURL != "" {
		token, stampedAt, err := s.Stamp(ctx, hashHex, item.TSAURL)
		if err != nil {
			stampErr = errs.Wrap(err, errs.CodeEvidenceTsaFailed, "evidence.Archive.stamp")
		} else {
			tsrKey := TSRKeyFor(emlKey)
			sumT := sha256.Sum256(token)
			if err := s.backend.PutLocked(ctx, tsrKey, bytes.NewReader(token), int64(len(token)), "application/timestamp-reply", s.lockMode, until); err != nil {
				stampErr = errs.Wrap(err, errs.CodeEvidenceTsaFailed, "evidence.Archive.tsr")
			} else {
				man.TSA = &ManifestTSA{At: stampedAt.UTC(), SHA256: hex.EncodeToString(sumT[:])}
				ref.TSA = true
				ref.TSRKey = tsrKey
			}
		}
	}
	if err := s.putManifest(ctx, manKey, man, until); err != nil {
		return Ref{}, err
	}
	if stampErr != nil {
		return ref, stampErr
	}
	return ref, nil
}

// Verify re-hashes the archived object and checks retention + optional TSA token.
func (s *Store) Verify(ctx context.Context, ref Ref, expectedSHA256 string) (ArchiveCheck, error) {
	var out ArchiveCheck
	out.TSA = nil
	if ref.Key == "" {
		return out, errs.New(errs.CodeValidationFailed, "Evidence ref.key is required.")
	}
	manKey := ref.ManifestKey
	if manKey == "" {
		manKey = ManifestKeyFor(ref.Key)
	}
	man, err := s.readManifest(ctx, manKey)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return out, errs.New(errs.CodeNotFound, "Evidence manifest not found.")
		}
		return out, errs.Wrap(err, errs.CodeInternalError, "evidence.Verify")
	}
	got, err := s.hashObject(ctx, ref.Key)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			out.Intact = false
			out.HashMatches = false
			return out, nil
		}
		return out, errs.Wrap(err, errs.CodeInternalError, "evidence.Verify")
	}
	out.Intact = got == man.SHA256
	want := strings.ToLower(strings.TrimSpace(expectedSHA256))
	if want == "" {
		want = man.SHA256
	}
	out.HashMatches = got == want && got == man.SHA256

	locked, until, err := s.backend.GetRetention(ctx, ref.Key)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return out, errs.Wrap(err, errs.CodeInternalError, "evidence.Verify.retention")
	}
	out.Locked = locked
	out.LockedUntil = until

	tsrKey := ref.TSRKey
	if tsrKey == "" {
		tsrKey = TSRKeyFor(ref.Key)
	}
	exists, err := s.backend.Exists(ctx, tsrKey)
	if err != nil {
		return out, errs.Wrap(err, errs.CodeInternalError, "evidence.Verify.tsr")
	}
	if !exists {
		out.TSA = nil
		return out, nil
	}
	rc, _, err := s.backend.Get(ctx, tsrKey)
	if err != nil {
		return out, errs.Wrap(err, errs.CodeInternalError, "evidence.Verify.tsrGet")
	}
	defer func() { _ = rc.Close() }()
	token, err := io.ReadAll(rc)
	if err != nil {
		return out, errs.Wrap(err, errs.CodeInternalError, "evidence.Verify.tsrRead")
	}
	info := &TSAInfo{}
	at, verr := s.verifyToken(token, man.SHA256, nil)
	if verr == nil {
		info.Valid = true
		t := at.UTC()
		info.At = &t
	} else if man.TSA != nil {
		t := man.TSA.At.UTC()
		info.At = &t
	}
	out.TSA = info
	return out, nil
}

func (s *Store) hashObject(ctx context.Context, key string) (string, error) {
	rc, _, err := s.backend.Get(ctx, key)
	if err != nil {
		return "", err
	}
	defer func() { _ = rc.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, rc); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (s *Store) readManifest(ctx context.Context, key string) (*Manifest, error) {
	rc, _, err := s.backend.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rc.Close() }()
	var m Manifest
	if err := json.NewDecoder(rc).Decode(&m); err != nil {
		return nil, fmt.Errorf("evidence: manifest decode: %w", err)
	}
	return &m, nil
}

func (s *Store) putManifest(ctx context.Context, key string, m Manifest, until time.Time) error {
	b, err := json.Marshal(m)
	if err != nil {
		return errs.Wrap(err, errs.CodeInternalError, "evidence.manifest")
	}
	if err := s.backend.PutLocked(ctx, key, bytes.NewReader(b), int64(len(b)), "application/json", s.lockMode, until); err != nil {
		return errs.Wrap(err, errs.CodeInternalError, "evidence.manifest.put")
	}
	return nil
}

func (s *Store) storeToken(ctx context.Context, emlKey, manKey string, man Manifest, token []byte, at time.Time, until time.Time) error {
	tsrKey := TSRKeyFor(emlKey)
	sum := sha256.Sum256(token)
	if err := s.backend.PutLocked(ctx, tsrKey, bytes.NewReader(token), int64(len(token)), "application/timestamp-reply", s.lockMode, until); err != nil {
		return err
	}
	man.TSA = &ManifestTSA{At: at.UTC(), SHA256: hex.EncodeToString(sum[:])}
	return s.putManifest(ctx, manKey, man, until)
}

// Open reads an archived object (the receipt raw API serves evidence first, S17 §8). The caller closes it.
func (s *Store) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	rc, _, err := s.backend.Get(ctx, key)
	if err != nil {
		return nil, errs.Wrap(err, errs.CodeNotFound, "evidence.Open")
	}
	return rc, nil
}
