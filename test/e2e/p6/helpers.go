//go:build e2e

package p6

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/test/e2e/harness"
	"github.com/radiobros/emailbridge/test/sink"
)

func p6Env(extra map[string]string) map[string]string {
	env := harness.P1Env(map[string]string{
		"EB_EGRESS_ALLOW":         "127.0.0.1/32,::1/128,localhost",
		"EB_EGRESS_BLOCK_PRIVATE": "false",
		"EB_GMAIL_PULL_INTERVAL":  "2s",
		"EB_GRAPH_PULL_INTERVAL":  "2s",
	})
	for k, v := range extra {
		env[k] = v
	}
	return env
}

// wrapGmailAPI proxies the Gmail simulator and adds sendAs (not in simulator/gmail).
func wrapGmailAPI(t *testing.T, simURL string) string {
	t.Helper()
	target, err := url.Parse(simURL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/settings/sendAs") {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"sendAs": []map[string]any{{
					"sendAsEmail": "sig@e2e.test", "displayName": "E2E Sig",
					"isDefault": true, "isPrimary": true, "signature": "<b>E2E</b>",
				}},
			})
			return
		}
		out := &http.Request{
			Method: r.Method, URL: &url.URL{
				Scheme: target.Scheme, Host: target.Host, Path: r.URL.Path, RawQuery: r.URL.RawQuery,
			},
			Header: r.Header.Clone(), Body: r.Body, ContentLength: r.ContentLength,
		}
		res, err := http.DefaultTransport.RoundTrip(out)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer func() { _ = res.Body.Close() }()
		for k, vv := range res.Header {
			for _, v := range vv {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(res.StatusCode)
		_, _ = io.Copy(w, res.Body)
	}))
	t.Cleanup(proxy.Close)
	return proxy.URL
}

func startBaseWithAPI(t *testing.T, gmailAPI, graphAPI string) *fixture {
	t.Helper()
	extra := map[string]string{}
	if gmailAPI != "" {
		extra["EB_GMAIL_API_URL"] = gmailAPI
	}
	if graphAPI != "" {
		extra["EB_GRAPH_API_URL"] = graphAPI
	}
	sim := harness.StartSim(t)
	n := harness.StartP1Node(t, p6Env(extra))
	owner, _ := n.OwnerSession(t)
	sk := harness.StartSink(t, sink.Options{})
	ep := owner.Do(http.MethodPost, "/v1/webhook-endpoints", map[string]any{
		"name": "p6-e2e", "kind": "http", "url": sk.URL, "events": []string{"*"},
		"secret":  harness.WebhookSecret(),
		"retry":   map[string]any{"maxAttempts": 3, "backoffBase": "200ms", "backoffJitter": 0.0, "timeout": "3s"},
		"payload": map[string]any{"text": "plain", "headers": []string{"Subject", "Message-ID"}},
	})
	harness.ExpectStatus(t, ep, http.StatusCreated)
	return &fixture{t: t, Node: n, Sim: sim, Owner: owner, Sink: sk, EPID: ep.Str("id")}
}

func waitAccountConnected(t *testing.T, owner *harness.Client, accID string, timeout time.Duration) {
	t.Helper()
	owner.WaitJSON(t, "/v1/accounts/"+accID, timeout, "connected", func(r harness.Resp) bool {
		return r.Status == http.StatusOK && (r.Str("state") == "connected" || r.Str("state") == "syncing")
	})
	owner.WaitJSON(t, "/v1/accounts/"+accID, timeout, "connected steady", func(r harness.Resp) bool {
		return r.Status == http.StatusOK && r.Str("state") == "connected"
	})
}

func nudgeSync(t *testing.T, owner *harness.Client, accID string) {
	t.Helper()
	r := owner.Do(http.MethodPost, "/v1/accounts/"+accID+"/sync", map[string]any{"mode": "full"})
	if r.Status != http.StatusAccepted && r.Status != http.StatusOK {
		t.Fatalf("sync nudge: %d %s", r.Status, r.Body)
	}
}

func waitMsgSubject(t *testing.T, owner *harness.Client, accID, subject string, timeout time.Duration) map[string]any {
	t.Helper()
	var found map[string]any
	owner.WaitJSON(t, "/v1/accounts/"+accID+"/messages?limit=50", timeout, "msg "+subject, func(r harness.Resp) bool {
		if r.Status != http.StatusOK {
			return false
		}
		data, _ := r.JSON["data"].([]any)
		for _, raw := range data {
			m, _ := raw.(map[string]any)
			if m != nil && m["subject"] == subject {
				found = m
				return true
			}
		}
		return false
	})
	return found
}

func mailboxByPath(t *testing.T, owner *harness.Client, accID, path string) string {
	t.Helper()
	r := owner.Do(http.MethodGet, "/v1/accounts/"+accID+"/mailboxes?limit=100", nil)
	harness.ExpectStatus(t, r, http.StatusOK)
	data, _ := r.JSON["data"].([]any)
	for _, raw := range data {
		m, _ := raw.(map[string]any)
		if m == nil {
			continue
		}
		if m["path"] == path || m["name"] == path {
			id, _ := m["id"].(string)
			return id
		}
	}
	t.Fatalf("mailbox %q not found: %s", path, r.Body)
	return ""
}

// waitOutboundSent returns true when the outbound reaches a success state.
// On timeout it logs BUG evidence (submit may still dial SMTP when Provider is dropped) and returns false.
func waitOutboundSent(t *testing.T, owner *harness.Client, sendID string, timeout time.Duration) bool {
	t.Helper()
	if sendID == "" {
		t.Fatal("empty outbound id")
		return false
	}
	deadline := time.Now().Add(timeout)
	var last harness.Resp
	for time.Now().Before(deadline) {
		last = owner.Do(http.MethodGet, "/v1/outbound-messages/"+sendID, nil)
		if last.Status == http.StatusOK {
			st := last.Str("state")
			if st == "sent" || st == "delivered" || last.JSON["sentAt"] != nil {
				return true
			}
			if st == "failed" || st == "held" || st == "cancelled" {
				t.Logf("BUG: outbound terminal %s — expected Gmail/Graph API send; body=%s — suspect cmd/emailbridge/wiring.go smtpSource.SMTP omits Provider — see summary", st, last.Body)
				return false
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Logf("BUG: outbound stayed queued (SMTP connect retries to dummy host) — expected users/…/sendMail or Gmail messages.send via provider path; body=%s — suspect cmd/emailbridge/wiring.go smtpSource.SMTP drops st.Provider so submit dials IMAP-shaped SMTP — see summary", last.Body)
	return false
}

func enableOrgSwitch(t *testing.T, owner *harness.Client, name string, value bool) {
	t.Helper()
	r := owner.Do(http.MethodPatch, "/v1/settings?scope=organization", map[string]any{
		"switches": map[string]any{name: map[string]any{"value": value}},
	})
	if r.Status != http.StatusOK {
		t.Fatalf("switch %s=%v: %d %s", name, value, r.Status, r.Body)
	}
}

func patchOrgSettings(t *testing.T, owner *harness.Client, values map[string]any) {
	t.Helper()
	r := owner.Do(http.MethodPatch, "/v1/settings?scope=organization", values)
	if r.Status != http.StatusOK {
		t.Fatalf("patch settings: %d %s", r.Status, r.Body)
	}
}

func problemCode(r harness.Resp) string { return r.Str("code") }

func etagOf(r harness.Resp) string {
	if e := r.Header.Get("ETag"); e != "" {
		return e
	}
	if v, ok := r.JSON["version"].(float64); ok {
		return fmt.Sprintf(`"%d"`, int64(v))
	}
	return ""
}

func withIfMatch(c *harness.Client, etag string, fn func() harness.Resp) harness.Resp {
	prev := c.Hdr
	hdr := map[string]string{}
	for k, v := range prev {
		hdr[k] = v
	}
	hdr["If-Match"] = etag
	c.Hdr = hdr
	r := fn()
	c.Hdr = prev
	return r
}

func orgBSession(t *testing.T, n *harness.Node, owner *harness.Client) (*harness.Client, string) {
	t.Helper()
	slug := fmt.Sprintf("org-b-p6-%d", time.Now().UnixNano()%1_000_000)
	ob := owner.Do(http.MethodPost, "/v1/organizations", map[string]any{"name": "Org B P6", "slug": slug})
	harness.ExpectStatus(t, ob, http.StatusCreated)
	orgB := ob.Str("id")
	email := fmt.Sprintf("member-b-%d@e2e.test", time.Now().UnixNano()%1_000_000)
	pass := harness.OwnerPassword
	token := n.SeedInvite(t, orgB, email, "owner")
	acc := n.NewClient()
	r := acc.Do(http.MethodPost, "/v1/invitations/accept", map[string]any{
		"token": token, "password": pass, "name": "Member B",
	})
	if r.Status != http.StatusOK && r.Status != http.StatusCreated {
		t.Fatalf("accept invite = %d %s", r.Status, r.Body)
	}
	bSess := n.NewClient()
	harness.ExpectStatus(t, bSess.SignIn(email, pass), http.StatusOK)
	secret, used := bSess.EnrolTOTP(pass)
	b := n.SignInWithTOTP(t, email, pass, secret, used)
	b.Hdr = map[string]string{"EB-Organization": orgB}
	return b, orgB
}

// mockIdP is a local authorization-code + refresh token endpoint for e2e OAuth.
type mockIdP struct {
	mu        sync.Mutex
	accessSeq int
	URL       string
	srv       *httptest.Server
}

func startMockIdP(t *testing.T) *mockIdP {
	t.Helper()
	m := &mockIdP{}
	mux := http.NewServeMux()
	mux.HandleFunc("/authorize", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		w.Header().Set("Content-Type", "application/json")
		switch r.Form.Get("grant_type") {
		case "authorization_code", "refresh_token", "urn:ietf:params:oauth:grant-type:jwt-bearer", "client_credentials":
			m.mu.Lock()
			m.accessSeq++
			n := m.accessSeq
			m.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token":  fmt.Sprintf("e2e-at-%d", n),
				"refresh_token": "e2e-rt-" + r.Form.Get("code"),
				"expires_in":    3600,
				"token_type":    "Bearer",
				"scope":         "mail",
			})
		default:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":"unsupported_grant_type"}`)
		}
	})
	mux.HandleFunc("/revoke", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	m.srv = httptest.NewServer(mux)
	m.URL = m.srv.URL
	t.Cleanup(m.srv.Close)
	return m
}

func (m *mockIdP) lastAccessToken() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.accessSeq == 0 {
		return ""
	}
	return fmt.Sprintf("e2e-at-%d", m.accessSeq)
}

func createOAuthApp(t *testing.T, owner *harness.Client, body map[string]any) harness.Resp {
	t.Helper()
	r := owner.Do(http.MethodPost, "/v1/oauth-apps", body)
	harness.ExpectStatus(t, r, http.StatusCreated)
	if r.Str("id") == "" {
		t.Fatalf("oauth app: %s", r.Body)
	}
	if _, ok := r.JSON["clientSecret"]; ok {
		t.Fatalf("clientSecret leaked: %s", r.Body)
	}
	return r
}

// apiProviderBody is a minimal POST /v1/accounts body for provider=gmail|graph.
// Huma requires nested imap/smtp fields; API providers skip live verify and do not dial these hosts.
func apiProviderBody(name, email, provider string, extra map[string]any) map[string]any {
	body := map[string]any{
		"name": name, "email": email, "provider": provider, "skipVerify": true,
		"imap": map[string]any{
			"host": "127.0.0.1", "port": 1993, "tls": "none",
			"auth": map[string]any{"user": email},
		},
		"smtp": map[string]any{
			"host": "127.0.0.1", "port": 1587, "tls": "none",
			"auth": map[string]any{"user": email},
		},
		"switches": map[string]any{"sandbox.enabled": false},
	}
	for k, v := range extra {
		body[k] = v
	}
	return body
}

func createIMAPAccount(t *testing.T, owner *harness.Client, sim *harness.SimEnv, user, pass, email, name string) string {
	t.Helper()
	sim.CreateSimUser(t, user, pass)
	body := harness.AccountCreateBody(t, sim, user, pass, email, name, map[string]any{"sandbox.enabled": false}, "standard")
	acc := owner.Do(http.MethodPost, "/v1/accounts", body)
	harness.ExpectStatus(t, acc, http.StatusCreated)
	id := acc.Str("id")
	owner.WaitJSON(t, "/v1/accounts/"+id, 90*time.Second, "connected", func(r harness.Resp) bool {
		return r.Status == http.StatusOK && r.Str("state") == "connected"
	})
	return id
}

func completeOAuth(t *testing.T, n *harness.Node, owner *harness.Client, accID, appID, provider string) {
	t.Helper()
	start := owner.Do(http.MethodPost, "/v1/accounts/"+accID+"/oauth/start", map[string]any{"appId": appID})
	harness.ExpectStatus(t, start, http.StatusOK)
	state := start.Str("state")
	if state == "" || start.Str("authorizeUrl") == "" {
		t.Fatalf("start: %s", start.Body)
	}
	cb := n.NewClient()
	r := cb.Do(http.MethodGet, "/callbacks/"+provider+"?code=e2e-code-1&state="+url.QueryEscape(state), nil)
	if r.Status != http.StatusFound && r.Status != http.StatusOK {
		t.Fatalf("callback status %d %s", r.Status, r.Body)
	}
	loc := r.Header.Get("Location")
	if loc != "" && !strings.Contains(loc, "oauth=connected") && !strings.Contains(loc, "oauth_error") {
		t.Fatalf("callback loc %s", loc)
	}
	if strings.Contains(loc, "oauth_error") {
		t.Fatalf("oauth callback error: %s", loc)
	}
}

func serviceAccountJSON(t *testing.T, tokenURI string) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	b, err := json.Marshal(map[string]string{
		"type": "service_account", "client_email": "sa@e2e.iam.gserviceaccount.com",
		"private_key": string(pemBytes), "token_uri": tokenURI,
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func tokenFromConnectURL(u string) string {
	u = strings.TrimRight(u, "/")
	i := strings.LastIndex(u, "/")
	if i < 0 {
		return u
	}
	return u[i+1:]
}

func verifyAuthServerHMAC(secret string, ts int64, body []byte, header string) bool {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(strconv.FormatInt(ts, 10)))
	_, _ = mac.Write([]byte("."))
	_, _ = mac.Write(body)
	want := "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(want), []byte(strings.TrimSpace(header)))
}

type fixture struct {
	t     *testing.T
	Node  *harness.Node
	Sim   *harness.SimEnv
	Owner *harness.Client
	Sink  *sink.Server
	EPID  string
}

func startBase(t *testing.T) *fixture {
	t.Helper()
	return startBaseWithAPI(t, "", "")
}
