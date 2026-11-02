# Okta SSO preset

OIDC defaults for Okta (`https://<okta-domain>/oauth2/default`, scopes including `groups`).

**Admin → Settings → OIDC:** **Start from a preset** → Okta. Register
`{publicURL}/v1/auth/oidc/<provider-id>/callback` in the Okta app after save.

Switch: `integrations.sso-okta`.
