package evidence

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/digitorus/timestamp"

	"github.com/radiobros/emailbridge/internal/errs"
)

// Stamp requests an RFC 3161 timestamp for the given SHA-256 hex digest against tsaURL.
func (s *Store) Stamp(ctx context.Context, sha256Hex, tsaURL string) ([]byte, time.Time, error) {
	hash, err := decodeSHA256(sha256Hex)
	if err != nil {
		return nil, time.Time{}, errs.New(errs.CodeValidationFailed, "sha256 must be 64 hex characters.")
	}
	tsaURL = strings.TrimSpace(tsaURL)
	if tsaURL == "" {
		return nil, time.Time{}, errs.New(errs.CodeValidationFailed, "tsaUrl is required.")
	}
	nonce, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 64))
	if err != nil {
		return nil, time.Time{}, errs.Wrap(err, errs.CodeInternalError, "evidence.Stamp.nonce")
	}
	req := &timestamp.Request{
		HashAlgorithm: crypto.SHA256,
		HashedMessage: hash,
		Certificates:  true,
		Nonce:         nonce,
	}
	der, err := req.Marshal()
	if err != nil {
		return nil, time.Time{}, errs.Wrap(err, errs.CodeInternalError, "evidence.Stamp.marshal")
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, tsaURL, bytes.NewReader(der))
	if err != nil {
		return nil, time.Time{}, errs.Wrap(err, errs.CodeEvidenceTsaFailed, "evidence.Stamp.request")
	}
	httpReq.Header.Set("Content-Type", "application/timestamp-query")
	resp, err := s.http.Do(httpReq)
	if err != nil {
		return nil, time.Time{}, errs.Wrap(err, errs.CodeEvidenceTsaFailed, "evidence.Stamp.do")
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, time.Time{}, errs.Wrap(err, errs.CodeEvidenceTsaFailed, "evidence.Stamp.read")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, time.Time{}, errs.New(errs.CodeEvidenceTsaFailed, fmt.Sprintf("TSA HTTP status %d", resp.StatusCode)).
			WithSafe("status", resp.StatusCode)
	}
	at, err := s.verifyToken(body, sha256Hex, nonce)
	if err != nil {
		return nil, time.Time{}, errs.Wrap(err, errs.CodeEvidenceTsaFailed, "evidence.Stamp.verify")
	}
	return body, at, nil
}

// verifyToken parses a .tsr, checks imprint (+ optional nonce) and anchors.
func (s *Store) verifyToken(token []byte, sha256Hex string, nonce *big.Int) (time.Time, error) {
	ts, err := timestamp.ParseResponse(token)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse: %w", err)
	}
	want, err := decodeSHA256(sha256Hex)
	if err != nil {
		return time.Time{}, err
	}
	if !bytes.Equal(ts.HashedMessage, want) {
		return time.Time{}, fmt.Errorf("message imprint mismatch")
	}
	if ts.HashAlgorithm != crypto.SHA256 {
		return time.Time{}, fmt.Errorf("unexpected hash algorithm")
	}
	if nonce != nil {
		if ts.Nonce == nil || ts.Nonce.Cmp(nonce) != 0 {
			return time.Time{}, fmt.Errorf("nonce mismatch")
		}
	}
	if len(s.tsaAnchors) > 0 {
		if err := verifyTSAChain(ts.Certificates, s.tsaAnchors); err != nil {
			return time.Time{}, err
		}
	}
	return ts.Time, nil
}

func verifyTSAChain(certs, anchors []*x509.Certificate) error {
	if len(certs) == 0 {
		return fmt.Errorf("TSA response has no certificates")
	}
	roots := x509.NewCertPool()
	inter := x509.NewCertPool()
	for _, a := range anchors {
		roots.AddCert(a)
	}
	leaf := certs[0]
	for _, c := range certs[1:] {
		inter.AddCert(c)
	}
	_, err := leaf.Verify(x509.VerifyOptions{
		Roots:         roots,
		Intermediates: inter,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageTimeStamping, x509.ExtKeyUsageAny},
		CurrentTime:   time.Now(),
	})
	if err != nil {
		return fmt.Errorf("TSA certificate chain: %w", err)
	}
	return nil
}

func decodeSHA256(hexStr string) ([]byte, error) {
	hexStr = strings.ToLower(strings.TrimSpace(hexStr))
	b, err := hex.DecodeString(hexStr)
	if err != nil || len(b) != sha256.Size {
		return nil, fmt.Errorf("invalid sha256 hex")
	}
	return b, nil
}

// LoadAnchorsPEM parses one or more PEM certificates (file contents or concatenated PEMs).
func LoadAnchorsPEM(pemBytes []byte) ([]*x509.Certificate, error) {
	var out []*x509.Certificate
	rest := pemBytes
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		c, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("evidence: parse TSA anchor: %w", err)
		}
		out = append(out, c)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("evidence: no certificates in TSA anchors PEM")
	}
	return out, nil
}

// LoadAnchorsFile reads EB_PEC_TSA_ANCHORS path (or inline PEM if it contains BEGIN CERTIFICATE).
func LoadAnchorsFile(pathOrPEM string) ([]*x509.Certificate, error) {
	pathOrPEM = strings.TrimSpace(pathOrPEM)
	if pathOrPEM == "" {
		return nil, nil
	}
	var raw []byte
	if strings.Contains(pathOrPEM, "BEGIN CERTIFICATE") {
		raw = []byte(pathOrPEM)
	} else {
		b, err := os.ReadFile(pathOrPEM) //nolint:gosec // EB_PEC_TSA_ANCHORS path from trusted config
		if err != nil {
			return nil, fmt.Errorf("evidence: read TSA anchors: %w", err)
		}
		raw = b
	}
	return LoadAnchorsPEM(raw)
}
