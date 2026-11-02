package objstore

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrBadKey is returned when an ID part is not a single safe path segment.
var ErrBadKey = errors.New("objstore: invalid key segment")

func segment(s string) error {
	if s == "" || strings.Contains(s, "/") || strings.Contains(s, `\`) || s == ".." || strings.Contains(s, "..") {
		return fmt.Errorf("%w: %q", ErrBadKey, s)
	}
	return nil
}

func check(parts ...string) error {
	for _, p := range parts {
		if err := segment(p); err != nil {
			return err
		}
	}
	return nil
}

// OutboundKey builds outbound/<org>/<account>/<outID>.eml (S1 §7).
func OutboundKey(org, account, outID string) (string, error) {
	if err := check(org, account, outID); err != nil {
		return "", err
	}
	return "outbound/" + org + "/" + account + "/" + outID + ".eml", nil
}

// ArchiveKey builds archive/<org>/<acc>/<yyyy>/<mm>/<msgId>.eml (S1 §7).
func ArchiveKey(org, account, msgID string, t time.Time) (string, error) {
	if err := check(org, account, msgID); err != nil {
		return "", err
	}
	t = t.UTC()
	return fmt.Sprintf("archive/%s/%s/%04d/%02d/%s.eml", org, account, t.Year(), int(t.Month()), msgID), nil
}

// SandboxKey builds sandbox/<org>/<capID>.eml (S1 §7).
func SandboxKey(org, capID string) (string, error) {
	if err := check(org, capID); err != nil {
		return "", err
	}
	return "sandbox/" + org + "/" + capID + ".eml", nil
}

// AttachmentKey builds attachments/<org>/<account>/<attID> (S1 §7).
func AttachmentKey(org, account, attID string) (string, error) {
	if err := check(org, account, attID); err != nil {
		return "", err
	}
	return "attachments/" + org + "/" + account + "/" + attID, nil
}

// ExportPrefix builds exports/<org>/<expID>/ (S1 §7).
func ExportPrefix(org, expID string) (string, error) {
	if err := check(org, expID); err != nil {
		return "", err
	}
	return "exports/" + org + "/" + expID + "/", nil
}

// BrandingKey builds branding/<org>/<assetID> (W68).
func BrandingKey(org, assetID string) (string, error) {
	if err := check(org, assetID); err != nil {
		return "", err
	}
	return "branding/" + org + "/" + assetID, nil
}

// BrandingPrefix builds branding/<org>/ for organisation erasure (W68).
func BrandingPrefix(org string) (string, error) {
	if err := check(org); err != nil {
		return "", err
	}
	return "branding/" + org + "/", nil
}
