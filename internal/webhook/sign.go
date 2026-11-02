package webhook

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/radiobros/emailbridge/pkg/webhookverify"
)

const secretPrefix = "whsec_"

// NewSecret returns a Standard Webhooks secret with 32 random bytes.
func NewSecret() (string, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return "", err
	}
	return secretPrefix + base64.StdEncoding.EncodeToString(key), nil
}

func validateSecret(secret string) error {
	if !strings.HasPrefix(secret, secretPrefix) {
		return errors.New("secret must start with whsec_")
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, secretPrefix))
	if err != nil || len(key) != 32 {
		return errors.New("secret must contain 32 base64-encoded bytes")
	}
	return nil
}

func secretID(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:8])
}

func secretPreview(secret string) string {
	if len(secret) <= 8 {
		return secret
	}
	return secret[:6] + "…" + secret[len(secret)-4:]
}

// SignatureHeaders signs body in standard or bodyOnly mode with every active secret.
func SignatureHeaders(mode, eventID string, at time.Time, body []byte, secrets []string) (http.Header, error) {
	if eventID == "" || len(secrets) == 0 {
		return nil, errors.New("webhook: event ID and signing secret are required")
	}
	if at.IsZero() {
		at = time.Now()
	}
	h := http.Header{}
	h.Set(webhookverify.HeaderWebhookID, eventID)
	signatures := make([]string, 0, len(secrets))
	switch mode {
	case SignatureBodyOnly:
		for _, secret := range secrets {
			if err := validateSecret(secret); err != nil {
				return nil, err
			}
			signatures = append(signatures, webhookverify.SignBodyOnly(secret, body))
		}
		h.Set(webhookverify.HeaderEBSignature, strings.Join(signatures, " "))
	case SignatureStandard, "":
		for _, secret := range secrets {
			if err := validateSecret(secret); err != nil {
				return nil, err
			}
			sig, err := webhookverify.Sign(secret, eventID, at, body)
			if err != nil {
				return nil, err
			}
			signatures = append(signatures, sig)
		}
		h.Set(webhookverify.HeaderWebhookTimestamp, formatUnix(at.Unix()))
		h.Set(webhookverify.HeaderWebhookSignature, strings.Join(signatures, " "))
	default:
		return nil, errors.New("webhook: unknown signature mode")
	}
	return h, nil
}

func formatUnix(v int64) string {
	if v == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	return string(b[i:])
}
