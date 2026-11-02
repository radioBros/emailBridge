//go:build e2e

package p6

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/radiobros/emailbridge/test/e2e/harness"
)

// External credential server (W54 G23): HMAC OK returns creds (not stored); bad HMAC → auth error.
//
// Note: sync installs an OAuth token source whenever useAuthServer is set (syncsource.go), so the
// credential server must also answer protocol=oauth with an accessToken; we register that token on
// the IMAP/SMTP simulator so XOAUTH2 succeeds.
func TestP6_AuthServer_HMAC(t *testing.T) {
	f := startBase(t)
	stamp := time.Now().UnixNano()
	user := fmt.Sprintf("as%d", stamp)
	pass := "sim-secret"
	email := user + "@sim.e2e.test"
	oauthTok := fmt.Sprintf("as-tok-%d", stamp)
	f.Sim.CreateSimUser(t, user, pass)
	f.Sim.Sim.SetOAuthToken(user, oauthTok)

	secret := "e2e-hmac-secret-key-32bytes-long!!"
	var hits atomic.Int64
	credSrv := startCredServerCounting(t, secret, user, pass, oauthTok, &hits)

	enableOrgSwitch(t, f.Owner, "accounts.authServer", true)
	patchOrgSettings(t, f.Owner, map[string]any{
		"authServer.url":        credSrv.URL,
		"authServer.hmacSecret": secret,
		"authServer.ttl":        "5m",
		"authServer.timeout":    "5s",
	})

	imapHost, imapPort := harness.HostPort(t, f.Sim.Addr.IMAPPlain)
	smtpHost, smtpPort := harness.HostPort(t, f.Sim.Addr.SMTPPlain)
	acc := f.Owner.Do(http.MethodPost, "/v1/accounts", map[string]any{
		"name": "AuthServer", "email": email, "externalId": "ext-" + user,
		"auth":       map[string]any{"useAuthServer": true},
		"skipVerify": true,
		"imap": map[string]any{
			"host": imapHost, "port": imapPort, "tls": "none",
			"auth": map[string]any{"user": user},
		},
		"smtp": map[string]any{
			"host": smtpHost, "port": smtpPort, "tls": "none",
			"auth": map[string]any{"user": user}, "policy": "standard",
		},
		"sync":     map[string]any{"paths": []string{"*"}, "idlePaths": []string{"INBOX"}},
		"switches": map[string]any{"sandbox.enabled": false, "accounts.authServer": true},
	})
	harness.ExpectStatus(t, acc, http.StatusCreated)
	accID := acc.Str("id")
	if acc.JSON["useAuthServer"] != true {
		t.Fatalf("create did not persist useAuthServer: %s", acc.Body)
	}

	var last harness.Resp
	ok := false
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		last = f.Owner.Do(http.MethodGet, "/v1/accounts/"+accID, nil)
		if last.Status == http.StatusOK && last.Str("state") == "connected" {
			ok = true
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !ok {
		t.Fatalf("authserver connected: state=%s reason=%v hits=%d body=%s",
			last.Str("state"), last.JSON["stateReason"], hits.Load(), last.Body)
	}
	if hits.Load() < 1 {
		t.Fatalf("credential server never called (hits=0)")
	}

	got := f.Owner.Do(http.MethodGet, "/v1/accounts/"+accID, nil)
	harness.ExpectStatus(t, got, http.StatusOK)
	raw := string(got.Body)
	if strings.Contains(raw, pass) || strings.Contains(raw, oauthTok) {
		t.Fatalf("secret leaked in account view: %s", raw)
	}
	imap, _ := got.JSON["imap"].(map[string]any)
	if imap != nil {
		if auth, _ := imap["auth"].(map[string]any); auth != nil {
			if _, has := auth["pass"]; has {
				t.Fatalf("imap.auth.pass present: %s", raw)
			}
			if auth["hasPassword"] == true {
				t.Fatalf("hasPassword true with auth server: %s", raw)
			}
		}
	}

	subj := fmt.Sprintf("authserver-%d", stamp)
	f.Sim.InjectRaw(t, user, "INBOX", harness.RawMessage(email, email, subj, "", "hi"), nil)
	f.Owner.WaitJSON(t, "/v1/accounts/"+accID+"/messages?limit=10", 90*time.Second, "msg", func(r harness.Resp) bool {
		data, _ := r.JSON["data"].([]any)
		for _, row := range data {
			m, _ := row.(map[string]any)
			if m != nil && m["subject"] == subj {
				return true
			}
		}
		return false
	})

	// Fresh account against a rejecting credential server (cache is process-local per URL+account).
	badHits := atomic.Int64{}
	bad := startCredServerCounting(t, "wrong-secret-not-matching!!!!!", user, pass, oauthTok, &badHits)
	patchOrgSettings(t, f.Owner, map[string]any{"authServer.url": bad.URL})
	user2 := fmt.Sprintf("asbad%d", stamp)
	f.Sim.CreateSimUser(t, user2, pass)
	f.Sim.Sim.SetOAuthToken(user2, oauthTok)
	badAcc := f.Owner.Do(http.MethodPost, "/v1/accounts", map[string]any{
		"name": "AuthServerBad", "email": user2 + "@sim.e2e.test", "externalId": "ext-" + user2,
		"auth": map[string]any{"useAuthServer": true}, "skipVerify": true,
		"imap": map[string]any{
			"host": imapHost, "port": imapPort, "tls": "none",
			"auth": map[string]any{"user": user2},
		},
		"smtp": map[string]any{
			"host": smtpHost, "port": smtpPort, "tls": "none",
			"auth": map[string]any{"user": user2}, "policy": "standard",
		},
		"sync":     map[string]any{"paths": []string{"*"}, "idlePaths": []string{"INBOX"}},
		"switches": map[string]any{"sandbox.enabled": false, "accounts.authServer": true},
	})
	harness.ExpectStatus(t, badAcc, http.StatusCreated)
	badID := badAcc.Str("id")
	f.Owner.WaitJSON(t, "/v1/accounts/"+badID, 60*time.Second, "auth server reject", func(r harness.Resp) bool {
		if r.Status != http.StatusOK {
			return false
		}
		st := r.Str("state")
		return st == "authError" || st == "connectError"
	})
	if badHits.Load() < 1 {
		t.Fatalf("rejecting credential server never called")
	}
}

func TestP6_AuthServer_SwitchOff_UsesPassword(t *testing.T) {
	f := startBase(t)
	stamp := time.Now().UnixNano()
	user := fmt.Sprintf("asoff%d", stamp)
	pass := "sim-secret"
	email := user + "@sim.e2e.test"
	accID := createIMAPAccount(t, f.Owner, f.Sim, user, pass, email, "No authserver")
	got := f.Owner.Do(http.MethodGet, "/v1/accounts/"+accID, nil)
	harness.ExpectStatus(t, got, http.StatusOK)
	if got.Str("state") != "connected" {
		t.Fatalf("state: %s", got.Body)
	}
}

func startCredServerCounting(t *testing.T, secret, user, pass, oauthTok string, hits *atomic.Int64) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		raw, _ := io.ReadAll(r.Body)
		ts, _ := strconv.ParseInt(r.Header.Get("X-EB-Timestamp"), 10, 64)
		if !verifyAuthServerHMAC(secret, ts, raw, r.Header.Get("X-EB-Signature")) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var req struct {
			Protocol string `json:"protocol"`
		}
		_ = json.Unmarshal(raw, &req)
		w.Header().Set("Content-Type", "application/json")
		switch strings.ToLower(req.Protocol) {
		case "oauth":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"username": user, "accessToken": oauthTok, "expiresIn": 300,
			})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"user": user, "pass": pass, "expiresIn": 300})
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}
