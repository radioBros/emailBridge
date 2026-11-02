package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/pquerna/otp/totp"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/store"
	"github.com/radiobros/emailbridge/test/sink"
)

// Info is printed as one JSON line for Playwright.
type Info struct {
	BaseURL string            `json:"baseUrl"`
	Owner   UserCred          `json:"owner"`
	Viewer  UserCred          `json:"viewer"`
	IDs     map[string]string `json:"ids"`
}

// UserCred is a seeded user.
type UserCred struct {
	Email      string `json:"email"`
	Password   string `json:"password"`
	TOTPSecret string `json:"totpSecret"`
}

func hostPort(addr string) (string, int) {
	h, p, err := net.SplitHostPort(addr)
	if err != nil {
		panic(err)
	}
	n, err := strconv.Atoi(p)
	if err != nil {
		panic(err)
	}
	return h, n
}

func webhookSecret() string {
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	return "whsec_" + base64.StdEncoding.EncodeToString(key)
}

func rawMessage(from, to, subject, messageID, body, contentType string) []byte {
	if messageID == "" {
		messageID = fmt.Sprintf("<%d@ui.test>", time.Now().UnixNano())
	}
	if contentType == "" {
		contentType = "text/plain; charset=utf-8"
	}
	return []byte(fmt.Sprintf(
		"From: %s\r\nTo: %s\r\nSubject: %s\r\nMessage-ID: %s\r\nDate: %s\r\nMIME-Version: 1.0\r\nContent-Type: %s\r\n\r\n%s\r\n",
		from, to, subject, messageID, time.Now().UTC().Format(time.RFC1123Z), contentType, body,
	))
}

func accountBody(st *Stack, user, pass, email, name string, switches map[string]any, smtpPolicy string) map[string]any {
	imapHost, imapPort := hostPort(st.SimAddr.IMAPPlain)
	smtpHost, smtpPort := hostPort(st.SimAddr.SMTPPlain)
	if switches == nil {
		switches = map[string]any{}
	}
	if smtpPolicy == "" {
		smtpPolicy = "standard"
	}
	return map[string]any{
		"name":  name,
		"email": email,
		"imap": map[string]any{
			"host": imapHost, "port": imapPort, "tls": "none",
			"auth": map[string]any{"user": user, "pass": pass},
		},
		"smtp": map[string]any{
			"host": smtpHost, "port": smtpPort, "tls": "none",
			"auth":   map[string]any{"user": user, "pass": pass},
			"policy": smtpPolicy,
		},
		"sync": map[string]any{
			"paths": []string{"*"}, "idlePaths": []string{"INBOX"},
		},
		"switches": switches,
	}
}

func enrolTOTP(c *Client, password string) string {
	r := c.Do(http.MethodPost, "/v1/auth/two-factor/initiate-setup", map[string]any{"password": password})
	c.mustOK(r, http.StatusOK, "initiate-setup")
	uri := r.Str("uri")
	secret := ""
	if i := strings.Index(uri, "secret="); i >= 0 {
		rest := uri[i+7:]
		if j := strings.IndexAny(rest, "&"); j >= 0 {
			secret = rest[:j]
		} else {
			secret = rest
		}
	}
	if secret == "" {
		panic("TOTP secret missing in " + uri)
	}
	code, _ := totp.GenerateCode(secret, time.Now())
	r = c.Do(http.MethodPost, "/v1/auth/two-factor/finalize-setup", map[string]any{"code": code})
	c.mustOK(r, http.StatusOK, "finalize-setup")
	// Burned this 30s Unix TOTP window for finalize — wait for the next one so
	// Playwright's first verify is not rejected as replay / already-used.
	now := time.Now()
	time.Sleep(time.Duration(30-now.Unix()%30)*time.Second + 500*time.Millisecond)
	return secret
}

func ownerSession(st *Stack) (*Client, string) {
	c := newClient(st.Base)
	r := c.Do(http.MethodPost, "/v1/auth/signin/credential", map[string]any{
		"credential": ownerEmail, "password": ownerPassword,
	})
	c.mustOK(r, http.StatusOK, "owner sign-in")
	me := c.Do(http.MethodGet, "/v1/me", nil)
	c.mustOK(me, http.StatusOK, "me")
	secret := ""
	if mfa, _ := me.JSON["mfa"].(bool); !mfa {
		secret = enrolTOTP(c, ownerPassword)
	}
	return c, secret
}

func seedInviteToken(st *Stack, orgID, email, role string) string {
	raw := make([]byte, 32)
	_, _ = rand.Read(raw)
	token := base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(token))
	hash := hex.EncodeToString(sum[:])
	now := time.Now().UTC()
	memID := store.NewID(store.PrefixMembership)
	doc := bson.M{
		"_id": memID, "organizationId": orgID, "email": strings.ToLower(email), "role": role,
		"invite":    bson.M{"email": strings.ToLower(email), "tokenHash": hash, "expiresAt": now.Add(72 * time.Hour)},
		"createdAt": now, "updatedAt": now, "version": int64(1),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client, err := mongo.Connect(options.Client().ApplyURI(st.MongoURI))
	if err != nil {
		panic(err)
	}
	defer func() { _ = client.Disconnect(context.Background()) }()
	_, err = client.Database(st.Database).Collection("memberships").InsertOne(ctx, doc)
	if err != nil {
		panic(err)
	}
	return token
}

func warn(what string, r Resp) {
	slog.Warn("uistack seed warn", "what", what, "status", r.Status, "body", truncate(string(r.Body), 240))
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func seed(st *Stack) Info {
	ids := map[string]string{}
	owner, totpSecret := ownerSession(st)
	ids["sessionCookie"] = sessionCookie

	orgs := owner.Do(http.MethodGet, "/v1/organizations?includeTotal=true", nil)
	owner.mustOK(orgs, http.StatusOK, "list orgs")
	orgA := ""
	if data, _ := orgs.JSON["data"].([]any); len(data) > 0 {
		if m, _ := data[0].(map[string]any); m != nil {
			orgA, _ = m["id"].(string)
		}
	}
	if orgA == "" {
		panic("bootstrap org missing")
	}
	ids["orgA"] = orgA
	owner.Hdr["EB-Organization"] = orgA

	orgB := owner.Do(http.MethodPost, "/v1/organizations", map[string]any{
		"name": "UI Org B",
		"slug": "ui-org-b",
	})
	if orgB.Status == http.StatusCreated {
		ids["orgB"] = orgB.Str("id")
	} else {
		warn("create org B", orgB)
	}

	// Viewer via invite accept (token from DB — API never returns plaintext).
	viewerInvite := seedInviteToken(st, orgA, viewerEmail, "viewer")
	accept := newClient(st.Base)
	ar := accept.Do(http.MethodPost, "/v1/invitations/accept", map[string]any{
		"token": viewerInvite, "password": viewerPassword, "name": "UI Viewer",
	})
	viewerSecret := ""
	if ar.Status == http.StatusOK || ar.Status == http.StatusCreated {
		vr := accept.Do(http.MethodPost, "/v1/auth/signin/credential", map[string]any{
			"credential": viewerEmail, "password": viewerPassword,
		})
		if vr.Status == http.StatusOK {
			me := accept.Do(http.MethodGet, "/v1/me", nil)
			if mfa, _ := me.JSON["mfa"].(bool); !mfa {
				viewerSecret = enrolTOTP(accept, viewerPassword)
			}
		}
	} else {
		warn("accept viewer invite", ar)
	}
	// Pending invite for the public accept-invite screen
	ids["inviteToken"] = seedInviteToken(st, orgA, "pending-invite@ui.test", "viewer")

	pass := "sim-secret"
	ctx := context.Background()

	// Account 1 — normal connected
	u1 := fmt.Sprintf("u1%d", time.Now().UnixNano())
	e1 := u1 + "@sim.ui.test"
	_ = st.SimCtl.CreateUser(ctx, u1, pass)
	acc1 := owner.Do(http.MethodPost, "/v1/accounts", accountBody(st, u1, pass, e1, "Inbox Alpha", map[string]any{
		"sandbox.enabled": false,
	}, "standard"))
	owner.mustOK(acc1, http.StatusCreated, "account 1")
	ids["accountId"] = acc1.Str("id")
	owner.waitJSON("/v1/accounts/"+ids["accountId"], 60*time.Second, "connected", func(r Resp) bool {
		return r.Status == http.StatusOK && (r.Str("state") == "connected" || r.Str("state") == "syncing")
	})

	// Account 2 — auth error (wrong password)
	u2 := fmt.Sprintf("u2%d", time.Now().UnixNano())
	e2 := u2 + "@sim.ui.test"
	_ = st.SimCtl.CreateUser(ctx, u2, pass)
	acc2Body := accountBody(st, u2, "wrong-password", e2, "Auth Error Box", map[string]any{
		"sandbox.enabled": false,
	}, "standard")
	acc2Body["skipVerify"] = true
	acc2 := owner.Do(http.MethodPost, "/v1/accounts", acc2Body)
	if acc2.Status == http.StatusCreated {
		ids["authErrorAccountId"] = acc2.Str("id")
		// Kick sync so the wrong password surfaces as authError without blocking create.
		_ = owner.Do(http.MethodPost, "/v1/accounts/"+acc2.Str("id")+"/sync", map[string]any{"mode": "full"})
	} else {
		warn("auth-error account", acc2)
	}

	// Account 3 — sandbox
	u3 := fmt.Sprintf("u3%d", time.Now().UnixNano())
	e3 := u3 + "@sim.ui.test"
	_ = st.SimCtl.CreateUser(ctx, u3, pass)
	acc3 := owner.Do(http.MethodPost, "/v1/accounts", accountBody(st, u3, pass, e3, "Sandbox Box", map[string]any{
		"sandbox.enabled": true,
	}, "standard"))
	if acc3.Status == http.StatusCreated {
		ids["sandboxAccountId"] = acc3.Str("id")
	}

	// Account 4 — PEC
	stamp := time.Now().UnixNano()
	pecUser := fmt.Sprintf("pec%d@pec.sim.test", stamp)
	pecRcpt := fmt.Sprintf("rcpt%d@pec.sim.test", stamp)
	_ = st.SimCtl.CreateUser(ctx, pecUser, pass)
	_ = st.SimCtl.CreateUser(ctx, pecRcpt, pass)
	accP := owner.Do(http.MethodPost, "/v1/accounts", accountBody(st, pecUser, pass, pecUser, "PEC Legal", map[string]any{
		"pec.enabled": true, "sandbox.enabled": false,
	}, "atMostOnce"))
	if accP.Status == http.StatusCreated {
		ids["pecAccountId"] = accP.Str("id")
		owner.waitJSON("/v1/accounts/"+ids["pecAccountId"], 60*time.Second, "pec connected", func(r Resp) bool {
			return r.Status == http.StatusOK && (r.Str("state") == "connected" || r.Str("state") == "syncing")
		})
		sent := owner.Do(http.MethodPost, "/v1/accounts/"+ids["pecAccountId"]+"/outbound-messages", map[string]any{
			"to":      []map[string]any{{"address": pecRcpt}},
			"subject": "UI PEC Verbale",
			"text":    "Certified hello from UI stack.",
		})
		if sent.Status == http.StatusAccepted {
			ids["pecOutboundId"] = sent.Str("id")
		} else {
			warn("pec send", sent)
		}
	} else {
		warn("pec account", accP)
	}

	// Messages: plain, HTML, thread-ish, attachment (multipart light)
	_, _ = st.SimCtl.InjectMessage(ctx, u1, "INBOX", rawMessage(e1, e1, "Hello plain", "", "Plain body", ""), nil, time.Now().UTC())
	_, _ = st.SimCtl.InjectMessage(ctx, u1, "INBOX", rawMessage(e1, e1, "Hello HTML", "", "<p><b>HTML</b> body</p>", "text/html; charset=utf-8"), nil, time.Now().UTC())
	mid := fmt.Sprintf("<thread-%d@ui.test>", stamp)
	_, _ = st.SimCtl.InjectMessage(ctx, u1, "INBOX", rawMessage(e1, e1, "Thread root", mid, "Root", ""), nil, time.Now().UTC())
	_, _ = st.SimCtl.InjectMessage(ctx, u1, "INBOX", []byte(fmt.Sprintf(
		"From: %s\r\nTo: %s\r\nSubject: Re: Thread root\r\nMessage-ID: <re-%d@ui.test>\r\nIn-Reply-To: %s\r\nReferences: %s\r\nDate: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nReply\r\n",
		e1, e1, stamp, mid, mid, time.Now().UTC().Format(time.RFC1123Z),
	)), nil, time.Now().UTC())
	_, _ = st.SimCtl.InjectMessage(ctx, u1, "INBOX", []byte(fmt.Sprintf(
		"From: %s\r\nTo: %s\r\nSubject: With attachment\r\nMessage-ID: <att-%d@ui.test>\r\nDate: %s\r\nMIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=b1\r\n\r\n--b1\r\nContent-Type: text/plain\r\n\r\nSee attached\r\n--b1\r\nContent-Type: text/plain; name=\"note.txt\"\r\nContent-Disposition: attachment; filename=\"note.txt\"\r\n\r\nattachment body\r\n--b1--\r\n",
		e1, e1, stamp, time.Now().UTC().Format(time.RFC1123Z),
	)), nil, time.Now().UTC())
	_ = owner.Do(http.MethodPost, "/v1/accounts/"+ids["accountId"]+"/sync", map[string]any{"mode": "full"})

	// Webhooks + deliveries
	okSink := sink.NewServer(sink.Options{})
	failSink := sink.NewServer(sink.Options{FailTimes: 50, FailStatus: 500})
	st.cleanups = append(st.cleanups, okSink.Close, failSink.Close)

	epOK := owner.Do(http.MethodPost, "/v1/webhook-endpoints", map[string]any{
		"name": "ui-ok", "kind": "http", "url": okSink.URL, "events": []string{"*"},
		"secret": webhookSecret(),
		"retry":  map[string]any{"maxAttempts": 3, "backoffBase": "200ms", "backoffJitter": 0.0, "timeout": "3s"},
	})
	if epOK.Status == http.StatusCreated {
		ids["webhookOkId"] = epOK.Str("id")
	} else {
		warn("webhook ok", epOK)
	}
	epFail := owner.Do(http.MethodPost, "/v1/webhook-endpoints", map[string]any{
		"name": "ui-fail", "kind": "http", "url": failSink.URL, "events": []string{"*"},
		"secret": webhookSecret(),
		"retry":  map[string]any{"maxAttempts": 2, "backoffBase": "100ms", "backoffJitter": 0.0, "timeout": "2s"},
	})
	if epFail.Status == http.StatusCreated {
		ids["webhookFailId"] = epFail.Str("id")
		_ = owner.Do(http.MethodPost, "/v1/routes", map[string]any{
			"name": "ui-fail-route", "endpointId": epFail.Str("id"), "filter": "true",
		})
	}

	if ids["webhookOkId"] != "" {
		rt := owner.Do(http.MethodPost, "/v1/routes", map[string]any{
			"name": "ui-ok-route", "endpointId": ids["webhookOkId"], "filter": `type.startsWith("message.")`,
		})
		if rt.Status == http.StatusCreated {
			ids["routeId"] = rt.Str("id")
		}
	}

	// Outbound in several states
	for _, subj := range []string{"Out queued", "Out delivered"} {
		r := owner.Do(http.MethodPost, "/v1/accounts/"+ids["accountId"]+"/outbound-messages", map[string]any{
			"to": []map[string]any{{"address": e1}}, "subject": subj, "text": "body",
		})
		if r.Status != http.StatusAccepted {
			warn("outbound "+subj, r)
		}
	}
	// cancelled / failed-ish: submit then cancel if API allows
	pending := owner.Do(http.MethodPost, "/v1/accounts/"+ids["accountId"]+"/outbound-messages", map[string]any{
		"to": []map[string]any{{"address": e1}}, "subject": "Out cancel me", "text": "body",
	})
	if pending.Status == http.StatusAccepted && pending.Str("id") != "" {
		ids["outboundId"] = pending.Str("id")
		_ = owner.Do(http.MethodPost, "/v1/outbound-messages/"+pending.Str("id")+"/cancel", map[string]any{})
	}

	// Templates
	tpl := owner.Do(http.MethodPost, "/v1/templates", map[string]any{
		"name": "Welcome", "subject": "Hello {{name}}", "text": "Hi {{name}}", "html": "<p>Hi {{name}}</p>",
	})
	if tpl.Status == http.StatusCreated {
		ids["templateId"] = tpl.Str("id")
	} else {
		warn("template", tpl)
	}

	// Gateways: SMTP + Mailgun API
	gw := owner.Do(http.MethodPost, "/v1/smtp-gateways", map[string]any{
		"name": "Sim SMTP", "kind": "smtp",
		"host": func() string { h, _ := hostPort(st.SimAddr.SMTPPlain); return h }(),
		"port": func() int { _, p := hostPort(st.SimAddr.SMTPPlain); return p }(),
		"tls":  "none",
		"auth": map[string]any{"user": u1, "pass": pass},
	})
	if gw.Status == http.StatusCreated {
		ids["gatewaySmtpId"] = gw.Str("id")
	} else {
		warn("smtp gateway", gw)
	}
	gwAPI := owner.Do(http.MethodPost, "/v1/smtp-gateways", map[string]any{
		"name": "Mailgun Lab", "kind": "mailgun",
		"api":         map[string]any{"domain": "mg.ui.test"},
		"credentials": map[string]any{"apiKey": "key-ui-test"},
	})
	if gwAPI.Status == http.StatusCreated {
		ids["gatewayApiId"] = gwAPI.Str("id")
	} else {
		warn("api gateway", gwAPI)
	}

	// Suppression
	sup := owner.Do(http.MethodPost, "/v1/suppression-lists", map[string]any{
		"name": "Global suppress", "description": "UI seed",
	})
	if sup.Status == http.StatusCreated {
		ids["suppressionListId"] = sup.Str("id")
		_ = owner.Do(http.MethodPost, "/v1/suppression-lists/"+sup.Str("id")+"/entries", map[string]any{
			"address": "blocked@example.test", "reason": "complaint",
		})
	}

	// Integrations — Brevo with required secret + from (empty config fails validation).
	brevo := owner.Do(http.MethodPost, "/v1/integrations/brevo/install", map[string]any{
		"name": "UI Brevo",
		"config": map[string]any{
			"from": "noreply@ui.test",
		},
		"secrets": map[string]string{ //nolint:gosec // G101: fake seed values for UI tests, not real credentials
			"apiKey": "xkeysib-ui-test-not-real",
		},
		"runTest": false,
	})
	if brevo.Status == http.StatusCreated || brevo.Status == http.StatusOK {
		ids["integrationId"] = brevo.Str("id")
	} else {
		warn("integration install brevo", brevo)
	}

	// API keys
	ak := owner.Do(http.MethodPost, "/v1/api-keys", map[string]any{
		"name": "ui-seed", "scopes": []string{"accounts:read", "messages:read"},
	})
	if ak.Status == http.StatusCreated {
		ids["apiKeyId"] = ak.Str("id")
	}

	// OAuth app (generic) — listed on the oauth-apps screen; not used for the
	// public /connect/:token page (that needs a hosted link, below).
	oa := owner.Do(http.MethodPost, "/v1/oauth-apps", map[string]any{ //nolint:gosec // G101: fake seed clientSecret for UI tests
		"name": "Generic Lab", "provider": "generic",
		"clientId": "ui-client", "clientSecret": "ui-secret",
		"authorizeUrl": "https://example.test/oauth/authorize",
		"tokenUrl":     "https://example.test/oauth/token",
		"scopes":       []string{"mail"},
	})
	if oa.Status == http.StatusCreated {
		ids["oauthAppId"] = oa.Str("id")
	} else {
		warn("oauth app", oa)
	}

	// Hosted connect page: enable the switch then mint a public link that
	// bootstraps via GET /v1/public/connect/{token} (oauth /v1/connect-links
	// tokens are a different flow and 404 on that endpoint).
	sw := owner.Do(http.MethodPatch, "/v1/settings?scope=organization", map[string]any{
		"switches": map[string]any{"connect.hostedPage": true},
	})
	if sw.Status != http.StatusOK {
		warn("enable connect.hostedPage", sw)
	}
	hosted := owner.Do(http.MethodPost, "/v1/connect-links/hosted", map[string]any{
		"ttlSeconds": 7200,
	})
	if hosted.Status == http.StatusCreated {
		ids["connectUrl"] = hosted.Str("url")
		if u := hosted.Str("url"); u != "" {
			parts := strings.Split(strings.TrimRight(u, "/"), "/")
			ids["connectToken"] = parts[len(parts)-1]
		}
	} else {
		warn("hosted connect link", hosted)
	}

	// Theme preference for owner
	_ = owner.Do(http.MethodPatch, "/v1/me/settings", map[string]any{
		"settings": map[string]any{"ui": map[string]any{"theme": "light", "locale": "en"}},
	})

	// Wait a bit for deliveries / PEC receipts to materialise
	time.Sleep(2 * time.Second)

	return Info{
		BaseURL: st.Base,
		Owner:   UserCred{Email: ownerEmail, Password: ownerPassword, TOTPSecret: totpSecret},
		Viewer:  UserCred{Email: viewerEmail, Password: viewerPassword, TOTPSecret: viewerSecret},
		IDs:     ids,
	}
}
