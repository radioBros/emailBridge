package accounts

import "github.com/danielgtaylor/huma/v2"

// OpenAPI examples for the account operations (W70). They are documentation only: the lint validates every
// one against the generated schema, so they are payloads the server really accepts or returns.

const (
	exampleAccountID  = "acc_01m5cvnpb031g57sqct6mzhq94"
	exampleAccount2ID = "acc_01m5x5dtxq189hak49x3b25a5h"
	exampleOrgID      = "org_01m5caqpk0kast63yc13wpy2d9"
	exampleJobID      = "job_01m5cnaee0w96fc46z350nc6q0"
	exampleUserID     = "usr_01m5ccvej0dqefkn0xhkcq2q6z"
	exampleCursor     = "eyJpZCI6ImFjY18wMW01eDVkdHhxMTg5aGFrNDl4M2IyNWE1aCIsImZpZWxkIjoiX2lkIiwiZGVzYyI6dHJ1ZX0"
)

// jsonContent is a media map with one unnamed example.
func jsonContent(v any) map[string]*huma.MediaType {
	return map[string]*huma.MediaType{"application/json": {Example: v}}
}

// jsonExamples is a media map with named examples.
func jsonExamples(ex map[string]*huma.Example) map[string]*huma.MediaType {
	return map[string]*huma.MediaType{"application/json": {Examples: ex}}
}

func exampleAccountView() map[string]any {
	return map[string]any{
		"id": exampleAccountID, "organizationId": exampleOrgID, "externalId": "crm-48213",
		"name": "Reception", "email": "reception@example.com", "state": "connected",
		"stateReason": map[string]any{"kind": "ok", "code": "indexed", "message": "Initial sync finished", "at": "2026-10-20T10:04:52Z"},
		"imap": map[string]any{
			"host": "imap.example.com", "port": 993, "tls": "implicit",
			"auth": map[string]any{"user": "reception@example.com", "hasPassword": true},
		},
		"smtp": map[string]any{
			"host": "smtp.example.com", "port": 465, "tls": "implicit", "policy": "standard",
			"auth": map[string]any{"user": "reception@example.com", "hasPassword": true},
		},
		"sync":     map[string]any{"paths": []any{"*"}, "idlePaths": []any{"INBOX"}, "resyncDelay": "5m"},
		"tz":       "Europe/Rome",
		"locale":   "it",
		"switches": map[string]any{"protocolLogs.enabled": true},
		"metadata": map[string]any{"crmId": "48213", "tier": 2, "vip": true},
		"identity": map[string]any{
			"email": "reception@example.com", "imapUser": "reception@example.com", "pinnedAt": "2026-10-20T10:02:15Z",
		},
		"lastSyncAt": "2026-10-27T09:12:44Z", "ownerNode": "eb-node-2",
		"createdAt": "2026-10-20T10:02:11Z", "updatedAt": "2026-10-27T09:12:44Z", "version": 3,
	}
}

func exampleOAuthAccountView() map[string]any {
	return map[string]any{
		"id": exampleAccount2ID, "organizationId": exampleOrgID,
		"name": "Sales team", "email": "sales@example.org", "state": "authError", "provider": "graph",
		"stateReason":      map[string]any{"kind": "auth", "code": "oauth_revoked", "message": "The refresh token was revoked", "at": "2026-10-26T08:14:03Z"},
		"authFailingSince": "2026-10-26T08:14:03Z",
		"imap": map[string]any{
			"host": "outlook.office365.com", "port": 993, "tls": "implicit",
			"auth": map[string]any{"user": "sales@example.org", "hasPassword": false},
		},
		"smtp": map[string]any{
			"host": "smtp.office365.com", "port": 587, "tls": "starttls",
			"auth": map[string]any{"user": "sales@example.org", "hasPassword": false},
		},
		"sync":        map[string]any{"paths": []any{"INBOX", "Sent Items"}, "idlePaths": []any{"INBOX"}},
		"lastSyncAt":  "2026-10-26T08:10:00Z",
		"lastErrorAt": "2026-10-26T08:14:03Z",
		"createdAt":   "2026-10-18T15:40:00Z", "updatedAt": "2026-10-26T08:14:03Z", "version": 7,
	}
}

func exampleAccountList() map[string]any {
	return map[string]any{
		"data":       []any{exampleAccountView(), exampleOAuthAccountView()},
		"nextCursor": exampleCursor,
	}
}

func exampleNewAccountView() map[string]any {
	v := exampleAccountView()
	v["state"] = "new"
	v["version"] = 1
	v["updatedAt"] = v["createdAt"]
	for _, k := range []string{"stateReason", "lastSyncAt", "ownerNode", "identity"} {
		delete(v, k)
	}
	return v
}

func exampleJobAccepted(kind string) map[string]any {
	return map[string]any{"job": map[string]any{
		"id": exampleJobID, "organizationId": exampleOrgID, "kind": kind, "state": "queued",
		"createdBy": exampleUserID, "createdAt": "2026-10-27T09:30:00Z",
	}}
}

func createAccountRequestExamples() map[string]*huma.Example {
	return map[string]*huma.Example{
		"imapPassword": {
			Summary:     "IMAP/SMTP with password",
			Description: "A classic mailbox. Both logins are tested before the account is saved.",
			Value: map[string]any{
				"name": "Reception", "email": "reception@example.com", "externalId": "crm-48213",
				"imap": map[string]any{
					"host": "imap.example.com", "port": 993, "tls": "implicit",
					"auth": map[string]any{"user": "reception@example.com", "pass": "app-password-from-provider"},
				},
				"smtp": map[string]any{
					"host": "smtp.example.com", "port": 465, "tls": "implicit",
					"auth": map[string]any{"user": "reception@example.com", "pass": "app-password-from-provider"},
				},
				"sync":     map[string]any{"paths": []any{"*"}, "idlePaths": []any{"INBOX"}},
				"tz":       "Europe/Rome",
				"switches": map[string]any{"protocolLogs.enabled": true},
				"metadata": map[string]any{"crmId": "48213", "tier": 2, "vip": true},
			},
		},
		"oauthAppOnly": {
			Summary:     "Microsoft 365 mailbox through an app-only OAuth app",
			Description: "No password: the account is bound to an app-only / service-account OAuth app, which mints tokens for the mailbox without user consent.",
			Value: map[string]any{
				"name": "Sales team", "email": "sales@example.org", "provider": "graph",
				"imap": map[string]any{
					"host": "outlook.office365.com", "port": 993, "tls": "implicit",
					"auth": map[string]any{"user": "sales@example.org"},
				},
				"smtp": map[string]any{
					"host": "smtp.office365.com", "port": 587, "tls": "starttls",
					"auth": map[string]any{"user": "sales@example.org"},
				},
				"oauth": map[string]any{"appId": "oap_01m55hx9fp0dr04jq9t64fvv6b"},
			},
		},
		"delegatedShared": {
			Summary:     "Shared mailbox opened with another account's OAuth",
			Description: "The token of account acc_01m5x5dtxq189hak49x3b25a5h (same organisation, OAuth-connected) opens the shared mailbox info@example.org.",
			Value: map[string]any{
				"name": "Info (shared)", "email": "info@example.org",
				"imap": map[string]any{
					"host": "outlook.office365.com", "port": 993, "tls": "implicit",
					"auth": map[string]any{"user": "info@example.org"},
				},
				"smtp": map[string]any{
					"host": "smtp.office365.com", "port": 587, "tls": "starttls",
					"auth": map[string]any{"user": "info@example.org"},
				},
				"delegated":  map[string]any{"account": exampleAccount2ID, "user": "info@example.org"},
				"skipVerify": true,
			},
		},
		"credentialServer": {
			Summary:     "Passwords from the organisation's credential server",
			Description: "useAuthServer: EmailBridge asks the configured credential server for the password on every login and stores none. Needs the accounts.authServer switch.",
			Value: map[string]any{
				"name": "Billing", "email": "billing@example.com",
				"imap": map[string]any{
					"host": "imap.example.com", "port": 993, "tls": "implicit",
					"auth": map[string]any{"user": "billing@example.com"},
				},
				"smtp": map[string]any{
					"host": "smtp.example.com", "port": 587, "tls": "starttls",
					"auth": map[string]any{"user": "billing@example.com"},
				},
				"auth":       map[string]any{"useAuthServer": true},
				"skipVerify": true,
			},
		},
	}
}

func patchAccountRequestExamples() map[string]*huma.Example {
	return map[string]*huma.Example{
		"rename": {
			Summary: "Rename and set the time zone",
			Value:   map[string]any{"name": "Front desk", "tz": "Europe/Rome", "externalId": "crm-48213"},
		},
		"newPassword": {
			Summary:     "Rotate the IMAP password",
			Description: "The imap object is sent whole; empty values keep what is stored. The password is re-encrypted and the sync node reconnects.",
			Value: map[string]any{"imap": map[string]any{
				"host": "imap.example.com", "port": 993, "tls": "implicit",
				"auth": map[string]any{"user": "reception@example.com", "pass": "new-app-password"},
			}},
		},
		"disable": {
			Summary: "Disable the account",
			Value:   map[string]any{"disabled": true},
		},
		"clearExternalId": {
			Summary: "Remove the external ID",
			Value:   map[string]any{"externalId": nil},
		},
		"metadata": {
			Summary:     "Change metadata (JSON Merge Patch)",
			Description: "Sets tier, removes legacyCode and keeps every other metadata key. Send \"metadata\": null to remove all metadata.",
			Value:       map[string]any{"metadata": map[string]any{"tier": 3, "legacyCode": nil}},
		},
	}
}

func verifyRequestExamples() map[string]*huma.Example {
	return map[string]*huma.Example{
		"both": {
			Summary: "Test IMAP and SMTP",
			Value: map[string]any{
				"imap": map[string]any{
					"host": "imap.example.com", "port": 993, "tls": "implicit",
					"auth": map[string]any{"user": "reception@example.com", "pass": "app-password-from-provider"},
				},
				"smtp": map[string]any{
					"host": "smtp.example.com", "port": 587, "tls": "starttls",
					"auth": map[string]any{"user": "reception@example.com", "pass": "app-password-from-provider"},
				},
			},
		},
		"smtpOnly": {
			Summary: "Test SMTP only, accepting a self-signed certificate",
			Value: map[string]any{
				"smtp": map[string]any{
					"host": "mail.internal.example.com", "port": 465, "tls": "implicit",
					"auth": map[string]any{"user": "relay@example.com", "pass": "relay-password"},
				},
				"ignoreCertErrors": true,
			},
		},
	}
}

func verifyResponseExamples() map[string]*huma.Example {
	return map[string]*huma.Example{
		"ok": {
			Summary: "Both logins succeeded",
			Value: map[string]any{
				"imap": map[string]any{
					"ok": true, "latencyMs": 412,
					"capabilities": []any{"IDLE", "IMAP4REV1", "MOVE", "QRESYNC", "UIDPLUS"},
					"specialUse":   map[string]any{"sent": "Sent", "drafts": "Drafts", "trash": "Trash", "junk": "Spam"},
				},
				"smtp": map[string]any{
					"ok": true, "latencyMs": 288, "capabilities": []any{"PIPELINING", "8BITMIME", "SMTPUTF8", "SIZE", "AUTH"},
				},
			},
		},
		"smtpAuthFailed": {
			Summary:     "IMAP works, SMTP login rejected",
			Description: "A failed test is still 200: each protocol reports ok, code and detail.",
			Value: map[string]any{
				"imap": map[string]any{"ok": true, "latencyMs": 395, "capabilities": []any{"IDLE", "IMAP4REV1", "UIDPLUS"}},
				"smtp": map[string]any{
					"ok": false, "latencyMs": 1630, "code": "mail_auth_failed",
					"detail": "535 5.7.8 Authentication credentials invalid",
				},
			},
		},
	}
}
