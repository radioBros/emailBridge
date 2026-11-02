# Google Workspace SSO preset

OIDC defaults for Google (`https://accounts.google.com`, scopes). No group claim; restrict via
`allowedEmailDomains` (HD) on the OIDC provider form.

**Admin → Settings → OIDC:** **Start from a preset** → Google Workspace. Register
`{publicURL}/v1/auth/oidc/<provider-id>/callback` in Google Cloud after save.

Switch: `integrations.sso-google`.
