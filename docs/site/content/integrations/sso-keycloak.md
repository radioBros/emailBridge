# Keycloak SSO preset

OIDC defaults for Keycloak (issuer pattern `https://<keycloak-host>/realms/<realm>`, scopes, `groups` claim).

**Admin → Settings → OIDC:** **Start from a preset** → Keycloak. Register
`{publicURL}/v1/auth/oidc/<provider-id>/callback` at the IdP after save.

Switch: `integrations.sso-keycloak`.
