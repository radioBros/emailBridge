package tracking

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
	tokenVersion = "1"
	KindOpen     = "o"
	KindClick    = "c"
	KindUnsub    = "u"
	maxTTL       = 365 * 24 * time.Hour
	defaultTTL   = 90 * 24 * time.Hour
)

var (
	errBadToken = errors.New("tracking: bad token")
	errExpired  = errors.New("tracking: expired token")
)

// Claims are the verified token fields.
type Claims struct {
	Kind       string
	OrgID      string
	OutboundID string
	Recipient  string
	Index      int
	Exp        time.Time
}

// Sign builds a compact HMAC token.
func Sign(key []byte, c Claims) (string, error) {
	if len(key) == 0 || c.OrgID == "" || c.OutboundID == "" || c.Recipient == "" {
		return "", errors.New("tracking: sign requires key, org, outbound and recipient")
	}
	switch c.Kind {
	case KindOpen, KindClick, KindUnsub:
	default:
		return "", errors.New("tracking: bad kind")
	}
	exp := c.Exp
	if exp.IsZero() {
		exp = time.Now().UTC().Add(defaultTTL)
	}
	payload := fmt.Sprintf("%s|%d|%s|%s|%s|%s|%d",
		tokenVersion, exp.UTC().Unix(), c.Kind, c.OrgID, c.OutboundID, strings.ToLower(c.Recipient), c.Index)
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(payload))
	sig := mac.Sum(nil)
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." +
		base64.RawURLEncoding.EncodeToString(sig), nil
}

// Verify checks HMAC and expiry.
func Verify(key []byte, token string, now time.Time) (Claims, error) {
	if len(key) == 0 || token == "" {
		return Claims{}, errBadToken
	}
	dot := strings.IndexByte(token, '.')
	if dot <= 0 || dot == len(token)-1 {
		return Claims{}, errBadToken
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(token[:dot])
	if err != nil {
		return Claims{}, errBadToken
	}
	sig, err := base64.RawURLEncoding.DecodeString(token[dot+1:])
	if err != nil {
		return Claims{}, errBadToken
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write(payloadBytes)
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return Claims{}, errBadToken
	}
	parts := strings.Split(string(payloadBytes), "|")
	if len(parts) != 7 || parts[0] != tokenVersion {
		return Claims{}, errBadToken
	}
	expUnix, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return Claims{}, errBadToken
	}
	if now.UTC().Unix() > expUnix {
		return Claims{}, errExpired
	}
	idx, err := strconv.Atoi(parts[6])
	if err != nil || idx < 0 {
		return Claims{}, errBadToken
	}
	kind := parts[2]
	if kind != KindOpen && kind != KindClick && kind != KindUnsub {
		return Claims{}, errBadToken
	}
	if parts[3] == "" || parts[4] == "" || parts[5] == "" {
		return Claims{}, errBadToken
	}
	return Claims{
		Kind: kind, OrgID: parts[3], OutboundID: parts[4], Recipient: parts[5],
		Index: idx, Exp: time.Unix(expUnix, 0).UTC(),
	}, nil
}

func mapTokenErr(err error) error {
	switch {
	case errors.Is(err, errExpired):
		return errs.New(errs.CodeSignedURLExpired, "The tracking token has expired.")
	case errors.Is(err, errBadToken):
		return errs.New(errs.CodeSignedURLInvalid, "The tracking token is invalid.")
	default:
		return errs.Wrap(err, errs.CodeSignedURLInvalid, "tracking.token")
	}
}

func clampTTL(d time.Duration) time.Duration {
	if d <= 0 {
		return defaultTTL
	}
	if d > maxTTL {
		return maxTTL
	}
	return d
}
