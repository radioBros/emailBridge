package gateways

import (
	"crypto/ecdsa"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const webhookReplayWindow = 15 * time.Minute

// verifyProviderSignature checks the provider webhook signature.
// For SES SubscriptionConfirmation it returns confirmURL to GET via egress.
// SES cryptographic verify is done separately via VerifySNSMessage when a client is available.
func verifyProviderSignature(kind, secret string, headers http.Header, queryToken string, body []byte, now time.Time) (ok bool, confirmURL string, err error) {
	switch kind {
	case KindMailgun:
		return verifyMailgun(secret, body, now)
	case KindSendGrid:
		return verifySendGrid(secret, headers, body, now), "", nil
	case KindPostmark:
		return secureEqual(secret, queryToken), "", nil
	case KindBrevo:
		tok := queryToken
		if tok == "" {
			tok = bearerToken(headers)
		}
		if tok == "" {
			tok = headers.Get("X-Brevo-Signature")
		}
		return secureEqual(secret, tok), "", nil
	case KindSES:
		return verifySES(headers, body, now)
	default:
		return false, "", fmt.Errorf("unknown kind")
	}
}

func secureEqual(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func verifyMailgun(secret string, body []byte, now time.Time) (bool, string, error) {
	if secret == "" {
		return false, "", nil
	}
	ts, token, sig := mailgunSigFields(body)
	if ts == "" || token == "" || sig == "" {
		return false, "", nil
	}
	unix, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return false, "", nil
	}
	if math.Abs(float64(now.Unix()-unix)) > webhookReplayWindow.Seconds() {
		return false, "", nil
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(ts + token))
	expectedHex := hex.EncodeToString(mac.Sum(nil))
	return subtle.ConstantTimeCompare([]byte(expectedHex), []byte(strings.ToLower(sig))) == 1 ||
		subtle.ConstantTimeCompare([]byte(expectedHex), []byte(sig)) == 1, "", nil
}

func mailgunSigFields(body []byte) (ts, token, sig string) {
	var nested struct {
		Signature struct {
			Timestamp string `json:"timestamp"`
			Token     string `json:"token"`
			Signature string `json:"signature"`
		} `json:"signature"`
	}
	if err := json.Unmarshal(body, &nested); err == nil && nested.Signature.Signature != "" {
		return nested.Signature.Timestamp, nested.Signature.Token, nested.Signature.Signature
	}
	var flat struct {
		Timestamp string `json:"timestamp"`
		Token     string `json:"token"`
		Signature string `json:"signature"`
	}
	if err := json.Unmarshal(body, &flat); err != nil {
		return "", "", ""
	}
	return flat.Timestamp, flat.Token, flat.Signature
}

func verifySendGrid(secret string, headers http.Header, body []byte, now time.Time) bool {
	if secret == "" {
		return false
	}
	sig := headers.Get("X-Twilio-Email-Event-Webhook-Signature")
	ts := headers.Get("X-Twilio-Email-Event-Webhook-Timestamp")
	if sig == "" || ts == "" {
		return false
	}
	unix, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return false
	}
	if math.Abs(float64(now.Unix()-unix)) > webhookReplayWindow.Seconds() {
		return false
	}
	pub, err := parseECDSAPublicKey(secret)
	if err != nil {
		return false
	}
	payload := append([]byte(ts), body...)
	sum := sha256.Sum256(payload)
	der, err := base64.StdEncoding.DecodeString(sig)
	if err != nil {
		return false
	}
	return ecdsa.VerifyASN1(pub, sum[:], der)
}

func parseECDSAPublicKey(secret string) (*ecdsa.PublicKey, error) {
	raw := strings.TrimSpace(secret)
	if strings.Contains(raw, "BEGIN") {
		block, _ := pem.Decode([]byte(raw))
		if block == nil {
			return nil, errors.New("pem")
		}
		pub, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			return nil, err
		}
		ek, ok := pub.(*ecdsa.PublicKey)
		if !ok {
			return nil, errors.New("not ecdsa")
		}
		return ek, nil
	}
	der, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, err
	}
	pub, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, err
	}
	ek, ok := pub.(*ecdsa.PublicKey)
	if !ok {
		return nil, errors.New("not ecdsa")
	}
	return ek, nil
}
