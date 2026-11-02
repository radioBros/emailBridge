//go:build e2e

// Package security is the active security suite of the final verification pass (PLAN "Definition of done"):
// it drives every operation of the generated OpenAPI document against a running node, so a new endpoint is
// covered without writing a test for it.
package security

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/radiobros/emailbridge/test/e2e/harness"
	"github.com/radiobros/emailbridge/test/sink"
)

type operation struct {
	method, path string
}

// publicPrefixes are the operations that answer without credentials by design (signed links, the hosted
// connect page, invitation acceptance, Limen auth, health, inbound callbacks).
var publicPrefixes = []string{
	"/a/", "/t/", "/u/", "/e/",
	"/healthz", "/readyz",
	"/callbacks/",
	"/integrations/",
	"/v1/public/",
	"/v1/invitations/accept", "/v1/invitations/lookup",
	// Exact public auth / connect routes only (session-required /v1/auth/* and connect-link admin ops stay checked).
	"/v1/auth/signin/", "/v1/auth/passkeys/begin-authentication", "/v1/auth/passkeys/finish-authentication",
	"/v1/auth/passwords/request-reset", "/v1/auth/passwords/reset", "/v1/auth/oidc/providers",
}

func isPublic(path string) bool {
	// The hosted connect redeem link is token-authenticated; the rest of /v1/connect-links/ needs a caller.
	// 2FA verify validates the body before the pending-session check: an empty body gets 422, a well-formed code
	// without a pending 2FA session gets 401 (verified 2026-10-28) — no information beyond "code is required".
	if path == "/v1/auth/two-factor/verify" {
		return true
	}
	// SSO start / callback are reached before sign-in.
	if strings.HasPrefix(path, "/v1/auth/oidc/") && (strings.HasSuffix(path, "/start") || strings.HasSuffix(path, "/callback")) {
		return true
	}
	if strings.HasPrefix(path, "/v1/connect-links/") && strings.HasSuffix(path, "/redeem") {
		return true
	}
	for _, p := range publicPrefixes {
		if p == "/healthz" || p == "/readyz" {
			if path == p {
				return true
			}
			continue
		}
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

func loadOperations(t *testing.T) []operation {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "api", "openapi", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Paths map[string]map[string]any `yaml:"paths"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	var out []operation
	for p, ops := range doc.Paths {
		for m := range ops {
			switch m {
			case "get", "post", "put", "patch", "delete":
				out = append(out, operation{strings.ToUpper(m), p})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].path+out[i].method < out[j].path+out[j].method })
	if len(out) < 100 {
		t.Fatalf("only %d operations in the document", len(out))
	}
	return out
}

var param = regexp.MustCompile(`\{(\w+)\}`)

// prefixes maps a path parameter to the ID prefix of the resource it names; unknown parameters get a word.
var prefixes = map[string]string{
	"accountId": "acc", "messageId": "msg", "mailboxId": "mbx", "draftId": "drf", "attachmentId": "att",
	"exportId": "exp", "outboundId": "out", "endpointId": "whe", "routeId": "rte", "deliveryId": "dlv",
	"eventId": "evt", "templateId": "tpl", "gatewayId": "gwy", "listId": "sup", "entryId": "spe",
	"captureId": "cap", "keyId": "key", "memberId": "mem", "organizationId": "org", "providerId": "oid",
	"appId": "oap", "instanceId": "int", "receiptId": "rcp", "jobId": "job", "logId": "log", "testId": "dtt",
	"sessionId": "ses",
}

// fill replaces path parameters with well-formed IDs that do not exist.
func fill(path string) string {
	return param.ReplaceAllStringFunc(path, func(m string) string {
		name := m[1 : len(m)-1]
		if p, ok := prefixes[name]; ok {
			return p + "_" + strings.Repeat("0", 26)
		}
		return "x"
	})
}

// Every operation that is not public refuses a caller without credentials with 401 — before validation,
// before the handler, before any lookup.
func TestSecurityUnauthenticatedRefused(t *testing.T) {
	n := harness.StartP1Node(t, nil)
	anon := n.NewClient()
	var bad []string
	for _, op := range loadOperations(t) {
		if isPublic(op.path) {
			continue
		}
		var body any
		if op.method != http.MethodGet && op.method != http.MethodDelete {
			body = map[string]any{}
		}
		r := anon.Do(op.method, fill(op.path), body)
		if r.Status != http.StatusUnauthorized {
			bad = append(bad, fmt.Sprintf("%s %s → %d %.120s", op.method, op.path, r.Status, r.Body))
		}
	}
	if len(bad) > 0 {
		t.Fatalf("%d operations answer an anonymous caller with something other than 401:\n%s", len(bad), strings.Join(bad, "\n"))
	}
}

// readAllowedWithAnyScope are reads an API key may make whatever its scopes (its own identity and settings).
var readAllowedWithAnyScope = map[string]bool{"/v1/me": true, "/v1/me/settings": true}

// An API key holding only logs:read cannot read anything else of the organisation (S10 §3).
func TestSecurityScopedKeyCannotReadOtherResources(t *testing.T) {
	n := harness.StartP1Node(t, nil)
	owner, _ := n.OwnerSession(t)
	_, token := owner.CreateAPIKey(t, "logs-only", []string{"logs:read"})
	key := n.WithBearer(token)
	var bad []string
	for _, op := range loadOperations(t) {
		if op.method != http.MethodGet || isPublic(op.path) || readAllowedWithAnyScope[op.path] || strings.HasPrefix(op.path, "/v1/logs") {
			continue
		}
		r := key.Do(op.method, fill(op.path), nil)
		// Item reads of unknown IDs may answer 404 before the scope check (nothing to protect); lists must 403.
		// Login-session routes (/v1/auth/*) never accept API keys: 401 is the refusal there.
		if strings.HasPrefix(op.path, "/v1/auth/") && r.Status == http.StatusUnauthorized {
			continue
		}
		if r.Status != http.StatusForbidden && (!strings.Contains(op.path, "{") || r.Status != http.StatusNotFound) {
			bad = append(bad, fmt.Sprintf("GET %s → %d %.120s", op.path, r.Status, r.Body))
		}
	}
	if len(bad) > 0 {
		t.Fatalf("%d reads are not refused for a logs:read key:\n%s", len(bad), strings.Join(bad, "\n"))
	}
}

// Secrets written through the API never come back in any read of the organisation: mail passwords, webhook
// secrets, gateway passwords.
func TestSecuritySecretsNeverReturned(t *testing.T) {
	sim := harness.StartSim(t)
	n := harness.StartP1Node(t, nil)
	owner, _ := n.OwnerSession(t)
	probe := fmt.Sprintf("Probe-S3cret-%d", os.Getpid())
	rnd := make([]byte, 32)
	_, _ = rand.Read(rnd)
	whsec := base64.StdEncoding.EncodeToString(rnd)

	user := "secprobe"
	sim.CreateSimUser(t, user, probe)
	body := harness.AccountCreateBody(t, sim, user, probe, user+"@sim.e2e.test", "Secret probe", map[string]any{"sandbox.enabled": false}, "standard")
	harness.ExpectStatus(t, owner.Do(http.MethodPost, "/v1/accounts", body), http.StatusCreated)
	hooks := harness.StartSink(t, sink.Options{})
	harness.ExpectStatus(t, owner.Do(http.MethodPost, "/v1/webhook-endpoints", map[string]any{
		"name": "probe", "kind": "http", "url": hooks.URL, "events": []string{"*"}, "secret": "whsec_" + whsec,
	}), http.StatusCreated)
	harness.ExpectStatus(t, owner.Do(http.MethodPost, "/v1/smtp-gateways", map[string]any{
		"name": "probe", "host": "smtp.example.test", "port": 587, "tls": "starttls",
		"auth": map[string]any{"user": "u", "pass": probe + "-gw"},
	}), http.StatusCreated)

	var leaks []string
	for _, op := range loadOperations(t) {
		if op.method != http.MethodGet || strings.Contains(op.path, "{") || isPublic(op.path) ||
			strings.HasSuffix(op.path, "/stream") || strings.HasPrefix(op.path, "/v1/logs/export") {
			continue
		}
		r := owner.Do(http.MethodGet, op.path, nil)
		if strings.Contains(string(r.Body), probe) || strings.Contains(string(r.Body), whsec) {
			leaks = append(leaks, op.path)
		}
	}
	// Item reads of what was created.
	for _, list := range []string{"/v1/accounts", "/v1/webhook-endpoints", "/v1/smtp-gateways"} {
		r := owner.Do(http.MethodGet, list, nil)
		data, _ := r.JSON["data"].([]any)
		for _, it := range data {
			m, _ := it.(map[string]any)
			id, _ := m["id"].(string)
			if id == "" {
				continue
			}
			if one := owner.Do(http.MethodGet, list+"/"+id, nil); strings.Contains(string(one.Body), probe) || strings.Contains(string(one.Body), whsec) {
				leaks = append(leaks, list+"/"+id)
			}
		}
	}
	if len(leaks) > 0 {
		t.Fatalf("a stored secret is returned by: %v", leaks)
	}
}

// Browser hardening: security headers on the SPA and the API, CSRF on cookie writes, SSRF refused at write.
func TestSecurityHeadersCSRFAndSSRF(t *testing.T) {
	n := harness.StartP1Node(t, nil)
	owner, _ := n.OwnerSession(t)

	for _, path := range []string{"/", "/v1/me"} {
		r := owner.Do(http.MethodGet, path, nil)
		h := r.Header
		if h.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: X-Content-Type-Options = %q", path, h.Get("X-Content-Type-Options"))
		}
		if h.Get("Referrer-Policy") == "" {
			t.Errorf("%s: no Referrer-Policy", path)
		}
		csp := h.Get("Content-Security-Policy")
		if !strings.Contains(csp, "frame-ancestors") && h.Get("X-Frame-Options") == "" {
			t.Errorf("%s: framing not restricted (CSP %q)", path, csp)
		}
	}

	// A cookie-authenticated write from a foreign origin without the CSRF header is refused.
	forged := owner.N.NewClient()
	forged.C.Jar = owner.C.Jar
	forged.Hdr = map[string]string{"Origin": "https://evil.example", "X-Requested-With": ""}
	if r := forged.Do(http.MethodPost, "/v1/api-keys", map[string]any{"name": "csrf", "scopes": []string{"logs:read"}}); r.Status < 400 {
		t.Errorf("cross-origin cookie write accepted: %d %s", r.Status, r.Body)
	}

	// Metadata and link-local destinations are refused for webhooks whatever the private-network setting.
	for _, u := range []string{"http://169.254.169.254/latest/meta-data/", "http://[fe80::1]/x", "http://metadata.google.internal/"} {
		r := owner.Do(http.MethodPost, "/v1/webhook-endpoints", map[string]any{
			"name": "ssrf", "kind": "http", "url": u, "events": []string{"*"}, "secret": "whsec_0123456789abcdef0123",
		})
		if r.Status < 400 {
			t.Errorf("webhook to %s accepted: %d", u, r.Status)
		}
	}

	// Sort keys are whitelisted; operator-looking query values are plain strings.
	for _, q := range []string{"/v1/accounts?sort=$where", "/v1/accounts?sort=name;drop", "/v1/events?sort=%7B%22$gt%22:1%7D"} {
		if r := owner.Do(http.MethodGet, q, nil); r.Status != http.StatusUnprocessableEntity && r.Status != http.StatusBadRequest {
			t.Errorf("%s → %d (want 400/422)", q, r.Status)
		}
	}
	if r := owner.Do(http.MethodGet, "/v1/accounts?q[$ne]=x", nil); r.Status >= 500 {
		t.Errorf("operator injection in q → %d", r.Status)
	}
}

// Unknown IDs are a 404 (or a validation problem), never a server error, for every item operation.
func TestSecurityUnknownIDsNeverServerError(t *testing.T) {
	n := harness.StartP1Node(t, nil)
	owner, _ := n.OwnerSession(t)
	var bad []string
	for _, op := range loadOperations(t) {
		if !strings.Contains(op.path, "{") || isPublic(op.path) || strings.HasSuffix(op.path, "/stream") {
			continue
		}
		var body any
		if op.method != http.MethodGet && op.method != http.MethodDelete {
			body = map[string]any{}
		}
		if r := owner.Do(op.method, fill(op.path), body); r.Status >= 500 {
			bad = append(bad, fmt.Sprintf("%s %s → %d %.160s", op.method, op.path, r.Status, r.Body))
		}
	}
	if len(bad) > 0 {
		t.Fatalf("%d operations fail with a server error on unknown IDs:\n%s", len(bad), strings.Join(bad, "\n"))
	}
}
