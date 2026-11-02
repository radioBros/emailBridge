package exports

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/radiobros/emailbridge/internal/errs"
)

const (
	tokenVersion = "v1"
	maxSignedTTL = 24 * time.Hour
	defaultTTL   = 15 * time.Minute
)

var (
	errBadToken = errors.New("exports: bad token")
	errExpired  = errors.New("exports: expired token")
)

// Sign builds a token for org + export that expires at exp.
func Sign(key []byte, orgID, expID string, exp time.Time) (string, error) {
	if len(key) == 0 || orgID == "" || expID == "" || exp.IsZero() {
		return "", errors.New("exports: sign requires key, org, export and expiry")
	}
	payload := fmt.Sprintf("%s|%d|%s|%s", tokenVersion, exp.UTC().Unix(), orgID, expID)
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(payload))
	sig := mac.Sum(nil)
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// VerifyToken checks the HMAC and expiry; returns orgID, exportID.
func VerifyToken(key []byte, token string, now time.Time) (orgID, expID string, err error) {
	if len(key) == 0 || token == "" {
		return "", "", errBadToken
	}
	dot := strings.IndexByte(token, '.')
	if dot <= 0 || dot == len(token)-1 {
		return "", "", errBadToken
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(token[:dot])
	if err != nil {
		return "", "", errBadToken
	}
	sig, err := base64.RawURLEncoding.DecodeString(token[dot+1:])
	if err != nil {
		return "", "", errBadToken
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(payloadBytes)
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return "", "", errBadToken
	}
	parts := strings.Split(string(payloadBytes), "|")
	if len(parts) != 4 || parts[0] != tokenVersion {
		return "", "", errBadToken
	}
	expUnix, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return "", "", errBadToken
	}
	if now.UTC().Unix() > expUnix {
		return "", "", errExpired
	}
	if parts[2] == "" || parts[3] == "" {
		return "", "", errBadToken
	}
	return parts[2], parts[3], nil
}

func mapTokenErr(err error) error {
	switch {
	case errors.Is(err, errExpired):
		return errs.New(errs.CodeSignedURLExpired, "The signed export URL has expired.")
	case errors.Is(err, errBadToken):
		return errs.New(errs.CodeSignedURLInvalid, "The signed export URL is invalid.")
	default:
		return errs.Wrap(err, errs.CodeSignedURLInvalid, "exports.token")
	}
}

func clampTTL(d time.Duration) time.Duration {
	if d <= 0 {
		return defaultTTL
	}
	if d > maxSignedTTL {
		return maxSignedTTL
	}
	return d
}
