package connectors

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// FeatherDeskSkew is the allowed clock skew for inbound callbacks.
const FeatherDeskSkew = 5 * time.Minute

// SignFeatherDesk builds X-FeatherDesk-Signature for timestamp + body.
func SignFeatherDesk(token string, ts int64, body []byte) string {
	mac := hmac.New(sha256.New, []byte(token))
	_, _ = fmt.Fprintf(mac, "%d.", ts)
	_, _ = mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// VerifyFeatherDeskCallback checks timestamp skew and HMAC of "<ts>.<body>".
func VerifyFeatherDeskCallback(token string, body []byte, tsHeader, sigHeader string, now time.Time) bool {
	token = strings.TrimSpace(token)
	tsHeader = strings.TrimSpace(tsHeader)
	sigHeader = strings.TrimSpace(sigHeader)
	if token == "" || tsHeader == "" || sigHeader == "" {
		return false
	}
	ts, err := strconv.ParseInt(tsHeader, 10, 64)
	if err != nil || ts <= 0 {
		return false
	}
	delta := now.Sub(time.Unix(ts, 0).UTC())
	if delta > FeatherDeskSkew || delta < -FeatherDeskSkew {
		return false
	}
	expected := SignFeatherDesk(token, ts, body)
	got := sigHeader
	if !strings.HasPrefix(strings.ToLower(got), "sha256=") {
		got = "sha256=" + got
	}
	return hmac.Equal([]byte(strings.ToLower(expected)), []byte(strings.ToLower(got)))
}
