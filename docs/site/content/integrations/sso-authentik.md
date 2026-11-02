# Authentik SSO preset

OIDC defaults for Authentik (issuer pattern, scopes, `groups` claim).

**Admin → Settings → OIDC:** use **Start from a preset** and pick Authentik. The form is prefilled;
you still review and save through the OIDC provider API. Register the redirect URI
`{publicURL}/v1/auth/oidc/<provider-id>/callback` at the IdP after save.

Install also writes organisation settings under `integrations.sso-authentik.*`.

Switch: `integrations.sso-authentik`.
