# Microsoft Entra ID SSO preset

OIDC defaults for Entra ID (`https://login.microsoftonline.com/<tenant-id>/v2.0`, scopes, `groups` / `roles` claims).

**Admin → Settings → OIDC:** **Start from a preset** → Microsoft Entra ID. Register
`{publicURL}/v1/auth/oidc/<provider-id>/callback` in the app registration after save.

Switch: `integrations.sso-entra`.
