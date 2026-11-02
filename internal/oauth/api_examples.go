package oauth

import "github.com/danielgtaylor/huma/v2"

// OpenAPI examples for the OAuth operations (W70). Documentation only; the lint validates each one against
// the generated schema.

func jsonContent(v any) map[string]*huma.MediaType {
	return map[string]*huma.MediaType{"application/json": {Example: v}}
}

func jsonExamples(ex map[string]*huma.Example) map[string]*huma.MediaType {
	return map[string]*huma.MediaType{"application/json": {Examples: ex}}
}

func exampleOutlookApp() map[string]any {
	return map[string]any{ //nolint:gosec // G101: documentation example OAuth ids, not real credentials.
		"id": "oap_01m55hx9fp0dr04jq9t64fvv6b", "organizationId": "org_01m5caqpk0kast63yc13wpy2d9",
		"name": "Microsoft 365 – Acme", "provider": "outlook",
		"clientId": "3f2b8c1e-6d4a-4b7f-9e21-5a0c8d7e6f13", "hasClientSecret": true, "hasServiceKey": false,
		"tenantId": "9c4e2a71-0b3d-4f6e-a8c5-2d1f7e9b6a40", "appOnly": false,
		"authorizeUrl": "https://login.microsoftonline.com/9c4e2a71-0b3d-4f6e-a8c5-2d1f7e9b6a40/oauth2/v2.0/authorize",
		"tokenUrl":     "https://login.microsoftonline.com/9c4e2a71-0b3d-4f6e-a8c5-2d1f7e9b6a40/oauth2/v2.0/token",
		"scopes": []any{
			"https://outlook.office.com/IMAP.AccessAsUser.All", "https://outlook.office.com/SMTP.Send", "offline_access",
		},
		"enabled": true, "createdAt": "2026-10-15T08:30:00Z", "updatedAt": "2026-10-22T17:05:12Z", "version": 2,
	}
}

func exampleGmailApp() map[string]any {
	return map[string]any{ //nolint:gosec // G101: documentation example OAuth client id, not a real credential.
		"id": "oap_01m5p28867bf01mrakx08k0aqp", "organizationId": "org_01m5caqpk0kast63yc13wpy2d9",
		"name": "Google Workspace – Acme", "provider": "gmail",
		"clientId": "418273650912-7k2m9q4v8x1c3b5n6p0r2s4t6w8y0z1a.apps.googleusercontent.com", "hasClientSecret": true,
		"hasServiceKey": true, "appOnly": false,
		"authorizeUrl": "https://accounts.google.com/o/oauth2/v2/auth", "tokenUrl": "https://oauth2.googleapis.com/token",
		"revokeUrl": "https://oauth2.googleapis.com/revoke", "scopes": []any{"https://mail.google.com/"},
		"enabled": true, "createdAt": "2026-10-12T11:00:00Z", "updatedAt": "2026-10-12T11:00:00Z", "version": 1,
	}
}

func exampleAppList() map[string]any {
	return map[string]any{
		"data":       []any{exampleOutlookApp(), exampleGmailApp()},
		"nextCursor": "eyJpZCI6Im9hcF8wMW01cDI4ODY3YmYwMW1yYWt4MDhrMGFxcCIsImZpZWxkIjoiX2lkIiwiZGVzYyI6dHJ1ZX0",
	}
}

func createAppRequestExamples() map[string]*huma.Example {
	return map[string]*huma.Example{
		"outlook": {
			Summary:     "Microsoft 365 (delegated, user consent)",
			Description: "The provider preset fills the endpoints and scopes; the tenant restricts sign-in to one Entra ID directory.",
			Value: map[string]any{ //nolint:gosec // G101: documentation example OAuth secret, not a real credential.
				"name": "Microsoft 365 – Acme", "provider": "outlook",
				"clientId": "3f2b8c1e-6d4a-4b7f-9e21-5a0c8d7e6f13", "clientSecret": "Qx8~vB2.mK7pL4sT9wR3yZ6nH1cF5dJ0gE",
				"tenantId": "9c4e2a71-0b3d-4f6e-a8c5-2d1f7e9b6a40",
			},
		},
		"outlookAppOnly": {
			Summary:     "Microsoft 365 app-only (client credentials)",
			Description: "Opens mailboxes without user consent; bind accounts to it with oauth.appId on POST /v1/accounts.",
			Value: map[string]any{ //nolint:gosec // G101: documentation example OAuth secret, not a real credential.
				"name": "Microsoft 365 – Acme (app-only)", "provider": "outlook", "appOnly": true,
				"clientId": "5d7a9c2e-1f3b-4e8d-b6a0-7c9e1f2d3b4a", "clientSecret": "Lp6~tR3.xB8nK1vM9sW4qZ7cH2dF5jG0yE",
				"tenantId": "9c4e2a71-0b3d-4f6e-a8c5-2d1f7e9b6a40",
			},
		},
		"gmailServiceAccount": {
			Summary:     "Google Workspace with a service-account key",
			Description: "serviceKey enables domain-wide delegation, so Workspace mailboxes connect without user consent.",
			Value: map[string]any{ //nolint:gosec // G101: documentation example OAuth / SA material, not real secrets.
				"name": "Google Workspace – Acme", "provider": "gmail",
				"clientId":     "418273650912-7k2m9q4v8x1c3b5n6p0r2s4t6w8y0z1a.apps.googleusercontent.com",
				"clientSecret": "GOCSPX-4f8Kq2mV7tZ1xR9pW3bN6cL0",
				"serviceKey":   `{"type":"service_account","client_email":"mail-sync@acme-mail.iam.gserviceaccount.com","private_key":"-----BEGIN PRIVATE KEY-----\nMIIEvQIBADANBgkqhkiG9w0BAQEFAASC…\n-----END PRIVATE KEY-----\n"}`,
			},
		},
		"generic": {
			Summary: "Any OAuth 2.0 mail provider",
			Value: map[string]any{ //nolint:gosec // G101: documentation example OAuth secret, not a real credential.
				"name": "Example Mail SSO", "provider": "generic",
				"clientId": "emailbridge", "clientSecret": "c2VjcmV0LWZyb20tZXhhbXBsZS1zc28",
				"authorizeUrl": "https://login.example.com/oauth2/authorize", "tokenUrl": "https://login.example.com/oauth2/token",
				"revokeUrl": "https://login.example.com/oauth2/revoke", "scopes": []any{"openid", "email", "mail.imap", "mail.smtp", "offline_access"},
			},
		},
	}
}
